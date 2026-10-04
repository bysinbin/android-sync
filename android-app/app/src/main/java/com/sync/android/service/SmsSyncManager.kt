package com.sync.android.service

import android.Manifest
import android.content.ContentValues
import android.content.Context
import android.content.pm.PackageManager
import android.database.ContentObserver
import android.net.Uri
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.provider.ContactsContract
import android.provider.Telephony
import android.telephony.SmsManager
import android.util.Log
import androidx.core.content.ContextCompat
import com.sync.android.model.SmsMessage

object SmsSyncManager {
    private const val TAG = "SmsSyncManager"
    private var contentObserver: ContentObserver? = null

    fun getContactName(context: Context, phoneNumber: String): String? {
        if (ContextCompat.checkSelfPermission(context, Manifest.permission.READ_CONTACTS) != PackageManager.PERMISSION_GRANTED) {
            return null
        }
        if (phoneNumber.isEmpty()) return null

        val uri = Uri.withAppendedPath(ContactsContract.PhoneLookup.CONTENT_FILTER_URI, Uri.encode(phoneNumber))
        val projection = arrayOf(ContactsContract.PhoneLookup.DISPLAY_NAME)
        return try {
            context.contentResolver.query(uri, projection, null, null, null)?.use { cursor ->
                if (cursor.moveToFirst()) {
                    cursor.getString(cursor.getColumnIndexOrThrow(ContactsContract.PhoneLookup.DISPLAY_NAME))
                } else null
            }
        } catch (e: Exception) {
            null
        }
    }

    fun fetchRecentMessages(context: Context, limit: Int = 100): List<SmsMessage> {
        if (ContextCompat.checkSelfPermission(context, Manifest.permission.READ_SMS) != PackageManager.PERMISSION_GRANTED) {
            Log.w(TAG, "READ_SMS izni verilmemiş!")
            return emptyList()
        }

        val messages = mutableListOf<SmsMessage>()
        val uri = Uri.parse("content://sms")
        val projection = arrayOf("_id", "thread_id", "address", "body", "date", "type", "read")
        val sortOrder = "date DESC LIMIT $limit"

        try {
            context.contentResolver.query(uri, projection, null, null, sortOrder)?.use { cursor ->
                val idIdx = cursor.getColumnIndex("_id")
                val threadIdx = cursor.getColumnIndex("thread_id")
                val addrIdx = cursor.getColumnIndex("address")
                val bodyIdx = cursor.getColumnIndex("body")
                val dateIdx = cursor.getColumnIndex("date")
                val typeIdx = cursor.getColumnIndex("type")
                val readIdx = cursor.getColumnIndex("read")

                while (cursor.moveToNext()) {
                    val id = if (idIdx >= 0) cursor.getString(idIdx) else ""
                    val threadId = if (threadIdx >= 0) cursor.getLong(threadIdx) else 0L
                    val address = if (addrIdx >= 0) cursor.getString(addrIdx) ?: "" else ""
                    val body = if (bodyIdx >= 0) cursor.getString(bodyIdx) ?: "" else ""
                    val date = if (dateIdx >= 0) cursor.getLong(dateIdx) else System.currentTimeMillis()
                    val type = if (typeIdx >= 0) cursor.getInt(typeIdx) else 1
                    val read = if (readIdx >= 0) cursor.getInt(readIdx) == 1 else true

                    val contact = getContactName(context, address)
                    val isIncoming = (type == Telephony.Sms.MESSAGE_TYPE_INBOX || type == 1)

                    messages.add(
                        SmsMessage(
                            id = id,
                            thread_id = threadId,
                            address = address,
                            contact_name = contact,
                            body = body,
                            timestamp = date,
                            is_incoming = isIncoming,
                            read = read
                        )
                    )
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "SMS sorgulama hatası: ${e.message}")
        }

        return messages
    }

    fun sendSms(context: Context, recipient: String, messageText: String): Pair<Boolean, String?> {
        if (ContextCompat.checkSelfPermission(context, Manifest.permission.SEND_SMS) != PackageManager.PERMISSION_GRANTED) {
            return Pair(false, "SEND_SMS izni verilmemiş!")
        }

        return try {
            val smsManager = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                context.getSystemService(SmsManager::class.java)
            } else {
                @Suppress("DEPRECATION")
                SmsManager.getDefault()
            }

            val parts = smsManager.divideMessage(messageText)
            if (parts.size > 1) {
                smsManager.sendMultipartTextMessage(recipient, null, parts, null, null)
            } else {
                smsManager.sendTextMessage(recipient, null, messageText, null, null)
            }

            // Gönderilen mesajı Android veritabanına kaydet
            try {
                val values = ContentValues().apply {
                    put("address", recipient)
                    put("body", messageText)
                    put("date", System.currentTimeMillis())
                    put("type", Telephony.Sms.MESSAGE_TYPE_SENT)
                    put("read", 1)
                }
                context.contentResolver.insert(Uri.parse("content://sms/sent"), values)
            } catch (e: Exception) {
                Log.w(TAG, "Sent SMS DB kaydı atlandı: ${e.message}")
            }

            Log.d(TAG, "SMS başarıyla gönderildi: $recipient")
            Pair(true, null)
        } catch (e: Exception) {
            Log.e(TAG, "SMS gönderme hatası: ${e.message}")
            Pair(false, e.message)
        }
    }

    fun startSmsObserver(context: Context, onNewSms: (SmsMessage) -> Unit) {
        if (contentObserver != null) return
        val handler = Handler(Looper.getMainLooper())

        contentObserver = object : ContentObserver(handler) {
            private var lastSeenId: String? = null

            override fun onChange(selfChange: Boolean, uri: Uri?) {
                super.onChange(selfChange, uri)
                val recent = fetchRecentMessages(context, 1)
                if (recent.isNotEmpty()) {
                    val latest = recent.first()
                    if (latest.id != lastSeenId) {
                        lastSeenId = latest.id
                        onNewSms(latest)
                    }
                }
            }
        }

        try {
            context.contentResolver.registerContentObserver(
                Uri.parse("content://sms"),
                true,
                contentObserver!!
            )
            Log.d(TAG, "SMS ContentObserver başlatıldı.")
        } catch (e: Exception) {
            Log.e(TAG, "SMS ContentObserver kayıt hatası: ${e.message}")
        }
    }

    fun stopSmsObserver(context: Context) {
        contentObserver?.let {
            try {
                context.contentResolver.unregisterContentObserver(it)
            } catch (e: Exception) {}
            contentObserver = null
        }
    }
}
