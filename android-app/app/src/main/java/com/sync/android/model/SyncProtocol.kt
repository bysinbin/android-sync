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
    const val NOTIFICATION_ACTION = "notification_action"
    const val NOTIFICATION_DISMISS = "notification_dismiss"
    const val TOUCHPAD_EVENT = "touchpad_event"
    const val BIOMETRIC_UNLOCK = "biometric_unlock"
    const val REMOTE_ACTION = "remote_action"
    const val OPEN_URL = "open_url"
    const val CONTACTS_REQUEST = "contacts_request"
    const val CONTACTS_RESPONSE = "contacts_response"
    const val PHOTOS_REQUEST = "photos_request"
    const val PHOTOS_RESPONSE = "photos_response"
    const val PHOTO_DOWNLOAD_REQUEST = "photo_download_request"
    const val SCREEN_MIRROR_REQUEST = "screen_mirror_request"
    const val SCREEN_MIRROR_FRAME = "screen_mirror_frame"
    const val SCREEN_TOUCH = "screen_touch"
    const val STORAGE_MOUNT_REQUEST = "storage_mount_request"
    const val STORAGE_MOUNT_STATUS = "storage_mount_status"
    const val HOTSPOT_COMMAND = "hotspot_command"
    const val HOTSPOT_STATUS = "hotspot_status"
    const val CALL_AUDIO_BRIDGE = "call_audio_bridge"
    const val APP_LIST_REQUEST = "app_list_request"
    const val APP_LIST_RESPONSE = "app_list_response"
    const val APP_LAUNCH_REQUEST = "app_launch_request"
    const val SCREEN_KEY_REQUEST = "screen_key"
    const val SCREEN_TEXT_REQUEST = "screen_text"
    const val SCREEN_DIM_REQUEST = "screen_dim"
    const val RINGER_COMMAND = "ringer_command"
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

data class NotificationActionItem(
    val index: Int,
    val title: String,
    val is_reply: Boolean = false
)

data class NotificationPayload(
    val id: String,
    val package_name: String,
    val app_name: String,
    val title: String,
    val text: String,
    val timestamp: Long,
    val key: String? = null,
    val can_reply: Boolean = false,
    val actions: List<NotificationActionItem>? = null
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

data class NotificationActionPayload(
    val notification_key: String,
    val action_index: Int
)

data class NotificationDismissPayload(
    val notification_key: String,
    val notification_id: String? = null
)

data class TouchpadEventPayload(
    val type: String, // "move", "click", "scroll", "key"
    val dx: Float? = null,
    val dy: Float? = null,
    val button: String? = null, // "left", "right", "middle"
    val scroll_y: Int? = null,
    val key: String? = null // "LEFT", "RIGHT", "UP", "DOWN", "F5", "ESC", "ENTER", "SPACE", "VOL_UP", "VOL_DOWN", "MUTE"
)

data class BiometricUnlockPayload(
    val status: String, // "REQUESTED", "AUTHENTICATED", "REJECTED"
    val unlock_pin: String? = null,
    val timestamp: Long = System.currentTimeMillis()
)

data class RemoteActionPayload(
    val action: String, // LOCK, SLEEP, SHUTDOWN, RESTART
    val param: String? = null
)

data class OpenUrlPayload(
    val url: String,
    val sender: String? = null
)

data class ContactItem(
    val id: String,
    val name: String,
    val number: String
)

data class ContactsResponsePayload(
    val contacts: List<ContactItem>
)

data class PhotoItem(
    val id: Long,
    val name: String,
    val date: Long,
    val size: Long,
    val mime_type: String,
    val width: Int = 0,
    val height: Int = 0,
    val thumbnail: String? = null
)

data class PhotosResponsePayload(
    val photos: List<PhotoItem>,
    val count: Int
)

data class PhotoDownloadRequestPayload(
    val id: Long,
    val upload_url: String? = null
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
    val text: String = "",
    val timestamp: Long = System.currentTimeMillis(),
    val type: String = "text", // "text" or "image"
    val image_base64: String? = null,
    val mime_type: String? = null
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

data class ScreenMirrorRequestPayload(
    val action: String, // START, STOP
    val quality: Int = 70
)

data class ScreenMirrorFramePayload(
    val width: Int,
    val height: Int,
    val data: String, // base64 JPEG
    val timestamp: Long = System.currentTimeMillis()
)

data class ScreenTouchPayload(
    val action: String, // down, move, up
    val x: Float, // 0.0 to 1.0
    val y: Float // 0.0 to 1.0
)

data class StorageMountRequestPayload(
    val action: String // START, STOP, STATUS
)

data class StorageMountStatusPayload(
    val enabled: Boolean,
    val port: Int,
    val url: String,
    val path: String
)

data class HotspotCommandPayload(
    val action: String // START, STOP, STATUS
)

data class HotspotStatusPayload(
    val enabled: Boolean,
    val ssid: String,
    val password: String,
    val ip: String? = null
)

data class CallAudioBridgePayload(
    val action: String, // START, STOP, DATA
    val direction: String? = null, // PHONE_TO_PC, PC_TO_PHONE
    val data: String? = null,
    val sample_rate: Int = 16000
)

data class InstalledAppInfo(
    val name: String,
    val package_name: String
)

data class AppListResponsePayload(
    val apps: List<InstalledAppInfo>
)

data class AppLaunchRequestPayload(
    val package_name: String
)

data class ScreenKeyPayload(
    val key_code: Int
)

data class ScreenTextPayload(
    val text: String
)

data class ScreenDimPayload(
    val enabled: Boolean
)

data class RingerCommandPayload(
    val mode: String // NORMAL, VIBRATE, SILENT
)

fun Any.toSyncMessage(event: String): String {

    val gson = Gson()
    val jsonTree = gson.toJsonTree(this)
    val msg = SyncMessage(event, jsonTree)
    return gson.toJson(msg)
}
