package com.sync.android.network

import android.content.Context
import android.os.BatteryManager
import android.os.Build
import android.util.Log
import com.google.gson.Gson
import com.sync.android.model.*
import okhttp3.*
import java.util.concurrent.TimeUnit

class SyncWebSocketClient(
    private val context: Context,
    private val onConnectionChanged: (isConnected: Boolean) -> Unit,
    private val onClipboardReceived: (text: String) -> Unit
) {
    private val TAG = "SyncWebSocketClient"
    private var webSocket: WebSocket? = null
    private val okHttpClient = OkHttpClient.Builder()
        .pingInterval(8, TimeUnit.SECONDS)
        .connectTimeout(5, TimeUnit.SECONDS)
        .readTimeout(0, TimeUnit.MILLISECONDS)
        .retryOnConnectionFailure(true)
        .build()

    var isConnected = false
        private set
    var isConnecting = false
        private set

    private var lastIp: String? = null
    private var lastPort: Int = 42424
    private val reconnectHandler = android.os.Handler(android.os.Looper.getMainLooper())
    private val reconnectRunnable = object : Runnable {
        override fun run() {
            if (!isConnected && !isConnecting && lastIp != null) {
                Log.d(TAG, "Otomatik yeniden bağlanma deneniyor: $lastIp:$lastPort")
                connect(lastIp!!, lastPort)
            }
            if (!isConnected) {
                reconnectHandler.postDelayed(this, 3000)
            }
        }
    }

    var onPhoneCommandReceived: ((action: String) -> Unit)? = null
    var onMediaInfoReceived: ((MediaInfoPayload) -> Unit)? = null

    @Synchronized
    fun connect(ip: String, port: Int) {
        lastIp = ip
        lastPort = port
        if (isConnected || isConnecting) return
        isConnecting = true

        val url = "ws://$ip:$port/ws"
        Log.d(TAG, "Connecting to $url...")

        val request = Request.Builder().url(url).build()
        webSocket = okHttpClient.newWebSocket(request, object : WebSocketListener() {
            override fun onOpen(ws: WebSocket, response: Response) {
                Log.d(TAG, "WebSocket connected successfully!")
                isConnecting = false
                isConnected = true
                reconnectHandler.removeCallbacks(reconnectRunnable)
                onConnectionChanged(true)
                sendDeviceInfo()
            }

            override fun onMessage(ws: WebSocket, text: String) {
                handleInboundMessage(text)
            }

            override fun onClosing(ws: WebSocket, code: Int, reason: String) {
                ws.close(1000, null)
            }

            override fun onClosed(ws: WebSocket, code: Int, reason: String) {
                Log.d(TAG, "WebSocket closed: $reason")
                isConnecting = false
                isConnected = false
                onConnectionChanged(false)
                reconnectHandler.removeCallbacks(reconnectRunnable)
                reconnectHandler.postDelayed(reconnectRunnable, 2000)
            }

            override fun onFailure(ws: WebSocket, t: Throwable, response: Response?) {
                Log.e(TAG, "WebSocket failure: ${t.message}")
                isConnecting = false
                isConnected = false
                onConnectionChanged(false)
                reconnectHandler.removeCallbacks(reconnectRunnable)
                reconnectHandler.postDelayed(reconnectRunnable, 2000)
            }
        })
    }

    private fun handleInboundMessage(json: String) {
        try {
            val msg = Gson().fromJson(json, SyncMessage::class.java)
            when (msg.event) {
                ProtocolEvents.CLIPBOARD -> {
                    val clip = Gson().fromJson(msg.payload, ClipboardPayload::class.java)
                    onClipboardReceived(clip.text)
                }
                ProtocolEvents.PHONE_COMMAND, ProtocolEvents.MEDIA_COMMAND -> {
                    val cmd = Gson().fromJson(msg.payload, PhoneCommandPayload::class.java)
                    if (cmd.action.startsWith("SEEK_PERCENT") && cmd.percent > 0) {
                        onPhoneCommandReceived?.invoke("SEEK_PERCENT:${cmd.percent}")
                    } else {
                        onPhoneCommandReceived?.invoke(cmd.action)
                    }
                }
                ProtocolEvents.MEDIA_INFO -> {
                    val info = Gson().fromJson(msg.payload, MediaInfoPayload::class.java)
                    onMediaInfoReceived?.invoke(info)
                }
                ProtocolEvents.PONG -> {
                    // Pong received
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "Error parsing message: ${e.message}")
        }
    }

    fun sendDeviceInfo() {
        val bm = context.getSystemService(Context.BATTERY_SERVICE) as? BatteryManager
        val level = bm?.getIntProperty(BatteryManager.BATTERY_PROPERTY_CAPACITY) ?: -1
        val isCharging = bm?.isCharging ?: false

        val payload = DeviceInfoPayload(
            device_name = Build.DEVICE,
            model = Build.MODEL,
            battery_level = level,
            is_charging = isCharging
        )
        sendMessage(payload.toSyncMessage(ProtocolEvents.DEVICE_INFO))
    }

    fun sendNotification(id: String, pkg: String, appName: String, title: String, text: String) {
        val payload = NotificationPayload(
            id = id,
            package_name = pkg,
            app_name = appName,
            title = title,
            text = text,
            timestamp = System.currentTimeMillis()
        )
        sendMessage(payload.toSyncMessage(ProtocolEvents.NOTIFICATION))
    }

    fun sendCallState(state: String, number: String, callerName: String = "") {
        val payload = CallStatePayload(
            state = state,
            phone_number = number,
            caller_name = callerName
        )
        sendMessage(payload.toSyncMessage(ProtocolEvents.CALL_STATE))
    }

    fun sendMediaCommand(action: String) {
        val payload = MediaCommandPayload(action = action)
        sendMessage(payload.toSyncMessage(ProtocolEvents.MEDIA_COMMAND))
    }

    fun sendMediaSeekPercent(percent: Double) {
        val payload = MediaCommandPayload(action = "SEEK_PERCENT", percent = percent)
        sendMessage(payload.toSyncMessage(ProtocolEvents.MEDIA_COMMAND))
    }

    fun sendMediaInfo(info: MediaInfoPayload) {
        sendMessage(info.toSyncMessage(ProtocolEvents.MEDIA_INFO))
    }

    fun sendClipboard(text: String) {
        val payload = ClipboardPayload(
            text = text,
            timestamp = System.currentTimeMillis()
        )
        sendMessage(payload.toSyncMessage(ProtocolEvents.CLIPBOARD))
    }

    fun sendMessage(json: String) {
        if (isConnected) {
            webSocket?.send(json)
        }
    }

    fun disconnect() {
        reconnectHandler.removeCallbacks(reconnectRunnable)
        isConnected = false
        webSocket?.close(1000, "Normal closure")
        webSocket = null
    }
}
