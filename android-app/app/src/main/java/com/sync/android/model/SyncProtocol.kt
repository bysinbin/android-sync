package com.sync.android.model

import com.google.gson.Gson
import com.google.gson.JsonElement

object ProtocolEvents {
    const val DEVICE_INFO = "device_info"
    const val NOTIFICATION = "notification"
    const val PC_NOTIFICATION = "pc_notification"
    const val CALL_STATE = "call_state"
    const val CALL_ACTION = "call_action"
    const val MEDIA_INFO = "media_info"
    const val MEDIA_COMMAND = "media_command"
    const val PHONE_COMMAND = "phone_command"
    const val CLIPBOARD = "clipboard"
    const val SMS_SYNC_REQUEST = "sms_sync_request"
    const val SMS_SYNC_RESPONSE = "sms_sync_response"
    const val SMS_SEND = "sms_send"
    const val SMS_SENT_STATUS = "sms_sent_status"
    const val SMS_NEW_MESSAGE = "sms_new_message"
    const val AUTH_REQUEST = "auth_request"
    const val AUTH_RESPONSE = "auth_response"
    const val PAIR_CONFIRM = "pair_confirm"
    const val UNPAIR = "unpair"
    const val FILE_AVAILABLE = "file_available"
    const val FILE_UPLOAD_NOTIFY = "file_upload_notify"
    const val NOTIFICATION_REPLY = "notification_reply"
    const val REMOTE_ACTION = "remote_action"
    const val PING = "ping"
    const val PONG = "pong"
}

data class AuthRequestPayload(
    val client_id: String,
    val client_name: String,
    val os: String,
    val auth_token: String? = null,
    val pairing_pin: String
)

data class AuthResponsePayload(
    val status: String, // "AUTHORIZED", "PAIRING_REQUIRED", "REJECTED"
    val client_name: String? = null
)

data class PairConfirmPayload(
    val approved: Boolean,
    val auth_token: String? = null,
    val device_name: String? = null,
    val client_id: String? = null
)


data class SyncMessage(
    val event: String,
    val payload: JsonElement
)

data class DeviceInfoPayload(
    val device_name: String,
    val model: String,
    val battery_level: Int,
    val is_charging: Boolean
)

data class NotificationPayload(
    val id: String,
    val package_name: String,
    val app_name: String,
    val title: String,
    val text: String,
    val timestamp: Long,
    val key: String? = null,
    val can_reply: Boolean = false
)

data class FileAvailablePayload(
    val id: String,
    val file_name: String,
    val file_size: Long,
    val download_url: String,
    val mime_type: String? = null,
    val sender: String,
    val timestamp: Long
)

data class FileUploadNotifyPayload(
    val id: String,
    val file_name: String,
    val file_size: Long,
    val path: String,
    val sender: String,
    val timestamp: Long
)

data class NotificationReplyPayload(
    val notification_key: String,
    val action_index: Int = 0,
    val reply_text: String
)

data class RemoteActionPayload(
    val action: String, // LOCK, SLEEP, SHUTDOWN, RESTART
    val param: String? = null
)

data class CallStatePayload(
    val state: String, // RINGING, OFFHOOK, IDLE
    val phone_number: String,
    val caller_name: String
)

data class CallActionPayload(
    val action: String, // ANSWER, REJECT, HANGUP, DIAL, SET_SPEAKER, SET_MUTE
    val number: String? = null,
    val value: Boolean? = null
)

data class MediaInfoPayload(
    val source: String = "", // "mac", "windows", or "phone"
    val title: String = "",
    val artist: String = "",
    val album: String = "",
    val is_playing: Boolean = false,
    val position_ms: Long = 0L,
    val duration_ms: Long = 0L,
    val percent: Double = 0.0
)

data class MediaCommandPayload(
    val action: String, // PLAY, PAUSE, PLAY_PAUSE, NEXT, PREVIOUS, VOLUME_UP, VOLUME_DOWN, SEEK_PERCENT
    val percent: Double = 0.0
)

data class PhoneCommandPayload(
    val action: String, // PLAY_PAUSE, NEXT, PREVIOUS, VOLUME_UP, VOLUME_DOWN, MUTE, RING, STOP_RING, SEEK_PERCENT
    val percent: Double = 0.0
)

data class ClipboardPayload(
    val text: String,
    val timestamp: Long
)

data class SmsMessage(
    val id: String,
    val thread_id: Long,
    val address: String,
    val contact_name: String?,
    val body: String,
    val timestamp: Long,
    val is_incoming: Boolean,
    val read: Boolean
)

data class SmsSyncPayload(
    val messages: List<SmsMessage>
)

data class SmsSendPayload(
    val recipient: String,
    val body: String
)

data class SmsSentStatusPayload(
    val success: Boolean,
    val recipient: String,
    val body: String,
    val error: String? = null,
    val timestamp: Long = System.currentTimeMillis()
)

data class DiscoveryPacket(
    val type: String,
    val server_name: String?,
    val ws_port: Int
)

fun Any.toSyncMessage(event: String): String {
    val gson = Gson()
    val jsonTree = gson.toJsonTree(this)
    val msg = SyncMessage(event, jsonTree)
    return gson.toJson(msg)
}
