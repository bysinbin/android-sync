package com.sync.android.model

import com.google.gson.Gson
import com.google.gson.JsonElement

object ProtocolEvents {
    const val DEVICE_INFO = "device_info"
    const val NOTIFICATION = "notification"
    const val CALL_STATE = "call_state"
    const val MEDIA_INFO = "media_info"
    const val MEDIA_COMMAND = "media_command"
    const val PHONE_COMMAND = "phone_command"
    const val CLIPBOARD = "clipboard"
    const val PING = "ping"
    const val PONG = "pong"
}

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
    val timestamp: Long
)

data class CallStatePayload(
    val state: String, // RINGING, OFFHOOK, IDLE
    val phone_number: String,
    val caller_name: String
)

data class MediaInfoPayload(
    val source: String = "", // "mac" or "phone"
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
