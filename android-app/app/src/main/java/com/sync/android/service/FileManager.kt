package com.sync.android.service

import android.app.DownloadManager
import android.content.Context
import android.net.Uri
import android.os.Environment
import android.provider.OpenableColumns
import android.util.Log
import android.widget.Toast
import com.sync.android.model.FileAvailablePayload
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.MultipartBody
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okio.BufferedSink
import okio.source
import java.io.File
import java.util.concurrent.TimeUnit

object FileManager {
    private const val TAG = "FileManager"

    private val httpClient = OkHttpClient.Builder()
        .connectTimeout(30, TimeUnit.SECONDS)
        .writeTimeout(60, TimeUnit.SECONDS)
        .readTimeout(60, TimeUnit.SECONDS)
        .build()

    /**
     * Downloads an incoming file sent from PC/Mac using Android DownloadManager.
     */
    fun downloadFile(context: Context, payload: FileAvailablePayload) {
        try {
            Log.d(TAG, "Dosya indirme başlatılıyor: ${payload.file_name} (${payload.download_url})")

            val dm = context.getSystemService(Context.DOWNLOAD_SERVICE) as? DownloadManager
            if (dm == null) {
                Log.e(TAG, "DownloadManager servisi alınamadı.")
                return
            }

            val uri = Uri.parse(payload.download_url)
            val request = DownloadManager.Request(uri).apply {
                setTitle(payload.file_name)
                setDescription("${payload.sender} üzerinden gönderildi (${formatFileSize(payload.file_size)})")
                setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
                setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, payload.file_name)
                setAllowedOverMetered(true)
                setAllowedOverRoaming(true)
                payload.mime_type?.let { setMimeType(it) }
            }

            dm.enqueue(request)
            CoroutineScope(Dispatchers.Main).launch {
                Toast.makeText(context, "📁 Dosya İndiriliyor: ${payload.file_name}", Toast.LENGTH_SHORT).show()
            }
        } catch (e: Exception) {
            Log.e(TAG, "Dosya indirme hatası: ${e.message}", e)
        }
    }

    /**
     * Uploads a file from Android to connected PC/Mac daemon via HTTP multipart.
     */
    fun uploadFile(
        context: Context,
        fileUri: Uri,
        serverBaseUrl: String, // e.g. "http://192.168.1.100:42424"
        onComplete: ((Boolean, String?) -> Unit)? = null
    ) {
        CoroutineScope(Dispatchers.IO).launch {
            try {
                val fileName = getFileName(context, fileUri) ?: "upload_${System.currentTimeMillis()}"
                val mimeType = context.contentResolver.getType(fileUri) ?: "application/octet-stream"

                val inputStream = context.contentResolver.openInputStream(fileUri)
                if (inputStream == null) {
                    withContext(Dispatchers.Main) {
                        onComplete?.invoke(false, "Dosya açılamadı")
                    }
                    return@launch
                }

                val requestBody = object : RequestBody() {
                    override fun contentType() = mimeType.toMediaTypeOrNull()

                    override fun writeTo(sink: BufferedSink) {
                        inputStream.source().use { source ->
                            sink.writeAll(source)
                        }
                    }
                }

                val multipartBody = MultipartBody.Builder()
                    .setType(MultipartBody.FORM)
                    .addFormDataPart("file", fileName, requestBody)
                    .build()

                val uploadUrl = "$serverBaseUrl/file/upload"
                val request = Request.Builder()
                    .url(uploadUrl)
                    .post(multipartBody)
                    .build()

                Log.d(TAG, "Dosya yükleniyor -> $uploadUrl ($fileName)")
                val response = httpClient.newCall(request).execute()

                val success = response.isSuccessful
                response.close()

                withContext(Dispatchers.Main) {
                    if (success) {
                        Toast.makeText(context, "✅ Bilgisayara Gönderildi: $fileName", Toast.LENGTH_SHORT).show()
                        onComplete?.invoke(true, null)
                    } else {
                        Toast.makeText(context, "❌ Gönderim başarısız: HTTP ${response.code}", Toast.LENGTH_SHORT).show()
                        onComplete?.invoke(false, "HTTP ${response.code}")
                    }
                }
            } catch (e: Exception) {
                Log.e(TAG, "Yükleme hatası: ${e.message}", e)
                withContext(Dispatchers.Main) {
                    Toast.makeText(context, "Hata: ${e.message}", Toast.LENGTH_SHORT).show()
                    onComplete?.invoke(false, e.message)
                }
            }
        }
    }

    private fun getFileName(context: Context, uri: Uri): String? {
        var name: String? = null
        if (uri.scheme == "content") {
            context.contentResolver.query(uri, null, null, null, null)?.use { cursor ->
                val nameIndex = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
                if (nameIndex != -1 && cursor.moveToFirst()) {
                    name = cursor.getString(nameIndex)
                }
            }
        }
        if (name == null) {
            name = uri.path?.let { File(it).name }
        }
        return name
    }

    private fun formatFileSize(bytes: Long): String {
        return when {
            bytes >= 1024 * 1024 * 1024 -> String.format("%.2f GB", bytes.toDouble() / (1024 * 1024 * 1024))
            bytes >= 1024 * 1024 -> String.format("%.2f MB", bytes.toDouble() / (1024 * 1024))
            bytes >= 1024 -> String.format("%.1f KB", bytes.toDouble() / 1024)
            else -> "$bytes B"
        }
    }
}
