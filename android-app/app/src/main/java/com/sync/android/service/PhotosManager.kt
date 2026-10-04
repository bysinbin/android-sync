package com.sync.android.service

import android.Manifest
import android.content.ContentUris
import android.content.Context
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.net.Uri
import android.os.Build
import android.provider.MediaStore
import android.util.Base64
import android.util.Log
import android.util.Size
import androidx.core.content.ContextCompat
import com.sync.android.model.PhotoItem
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.MultipartBody
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.ByteArrayOutputStream
import java.util.concurrent.TimeUnit

class PhotosManager(private val context: Context) {

    companion object {
        private const val TAG = "PhotosManager"
    }

    private val httpClient = OkHttpClient.Builder()
        .connectTimeout(15, TimeUnit.SECONDS)
        .writeTimeout(60, TimeUnit.SECONDS)
        .readTimeout(60, TimeUnit.SECONDS)
        .build()

    fun hasPermission(): Boolean {
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            ContextCompat.checkSelfPermission(context, Manifest.permission.READ_MEDIA_IMAGES) == PackageManager.PERMISSION_GRANTED
        } else {
            ContextCompat.checkSelfPermission(context, Manifest.permission.READ_EXTERNAL_STORAGE) == PackageManager.PERMISSION_GRANTED
        }
    }

    fun fetchRecentPhotos(limit: Int = 40): List<PhotoItem> {
        if (!hasPermission()) {
            Log.w(TAG, "Fotoğraf okuma izni verilmemiş")
            return emptyList()
        }

        val photos = mutableListOf<PhotoItem>()
        val projection = arrayOf(
            MediaStore.Images.Media._ID,
            MediaStore.Images.Media.DISPLAY_NAME,
            MediaStore.Images.Media.DATE_ADDED,
            MediaStore.Images.Media.SIZE,
            MediaStore.Images.Media.MIME_TYPE,
            MediaStore.Images.Media.WIDTH,
            MediaStore.Images.Media.HEIGHT
        )

        val sortOrder = "${MediaStore.Images.Media.DATE_ADDED} DESC"

        try {
            context.contentResolver.query(
                MediaStore.Images.Media.EXTERNAL_CONTENT_URI,
                projection,
                null,
                null,
                sortOrder
            )?.use { cursor ->
                val idCol = cursor.getColumnIndexOrThrow(MediaStore.Images.Media._ID)
                val nameCol = cursor.getColumnIndexOrThrow(MediaStore.Images.Media.DISPLAY_NAME)
                val dateCol = cursor.getColumnIndexOrThrow(MediaStore.Images.Media.DATE_ADDED)
                val sizeCol = cursor.getColumnIndexOrThrow(MediaStore.Images.Media.SIZE)
                val mimeCol = cursor.getColumnIndexOrThrow(MediaStore.Images.Media.MIME_TYPE)
                val widthCol = cursor.getColumnIndexOrThrow(MediaStore.Images.Media.WIDTH)
                val heightCol = cursor.getColumnIndexOrThrow(MediaStore.Images.Media.HEIGHT)

                var count = 0
                while (cursor.moveToNext() && count < limit) {
                    val id = cursor.getLong(idCol)
                    val name = cursor.getString(nameCol) ?: "IMG_$id.jpg"
                    val dateAddedSec = cursor.getLong(dateCol)
                    val size = cursor.getLong(sizeCol)
                    val mime = cursor.getString(mimeCol) ?: "image/jpeg"
                    val width = cursor.getInt(widthCol)
                    val height = cursor.getInt(heightCol)

                    val contentUri = ContentUris.withAppendedId(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, id)
                    val thumbnailBase64 = generateThumbnailBase64(contentUri, id)

                    photos.add(
                        PhotoItem(
                            id = id,
                            name = name,
                            date = dateAddedSec * 1000L,
                            size = size,
                            mime_type = mime,
                            width = width,
                            height = height,
                            thumbnail = thumbnailBase64
                        )
                    )
                    count++
                }
            }
            Log.d(TAG, "Toplam ${photos.size} adet fotoğraf başarıyla tarandı.")
        } catch (e: Exception) {
            Log.e(TAG, "Fotoğraf listesi okunurken hata oluştu", e)
        }

        return photos
    }

    private fun generateThumbnailBase64(uri: Uri, id: Long): String? {
        return try {
            val bitmap: Bitmap? = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                try {
                    context.contentResolver.loadThumbnail(uri, Size(240, 240), null)
                } catch (e: Exception) {
                    null
                }
            } else {
                @Suppress("DEPRECATION")
                MediaStore.Images.Thumbnails.getThumbnail(
                    context.contentResolver,
                    id,
                    MediaStore.Images.Thumbnails.MINI_KIND,
                    null
                )
            }

            bitmap?.let { bmp ->
                val stream = ByteArrayOutputStream()
                bmp.compress(Bitmap.CompressFormat.JPEG, 65, stream)
                val bytes = stream.toByteArray()
                Base64.encodeToString(bytes, Base64.NO_WRAP)
            }
        } catch (e: Exception) {
            Log.w(TAG, "Thumbnail oluşturulamadı ($uri): ${e.message}")
            null
        }
    }

    fun uploadPhotoToHost(photoId: Long, uploadUrl: String): Boolean {
        try {
            val contentUri = ContentUris.withAppendedId(MediaStore.Images.Media.EXTERNAL_CONTENT_URI, photoId)
            var filename = "photo_$photoId.jpg"
            var mimeType = "image/jpeg"

            context.contentResolver.query(
                contentUri,
                arrayOf(MediaStore.Images.Media.DISPLAY_NAME, MediaStore.Images.Media.MIME_TYPE),
                null,
                null,
                null
            )?.use { cursor ->
                if (cursor.moveToFirst()) {
                    filename = cursor.getString(0) ?: filename
                    mimeType = cursor.getString(1) ?: mimeType
                }
            }

            val inputStream = context.contentResolver.openInputStream(contentUri) ?: return false
            val bytes = inputStream.use { it.readBytes() }

            val requestBody = MultipartBody.Builder()
                .setType(MultipartBody.FORM)
                .addFormDataPart(
                    "files",
                    filename,
                    bytes.toRequestBody(mimeType.toMediaTypeOrNull())
                )
                .build()

            val request = Request.Builder()
                .url(uploadUrl)
                .post(requestBody)
                .build()

            httpClient.newCall(request).execute().use { response ->
                if (response.isSuccessful) {
                    Log.i(TAG, "Fotoğraf başarıyla aktarıldı: $filename -> $uploadUrl")
                    return true
                } else {
                    Log.e(TAG, "Fotoğraf yüklenemedi: HTTP ${response.code}")
                    return false
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "Fotoğraf aktarımı sırasında hata oluştu", e)
            return false
        }
    }
}
