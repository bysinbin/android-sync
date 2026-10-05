package com.sync.android.service

import android.content.Context
import android.os.BatteryManager
import android.os.Build
import android.util.Log
import com.google.gson.Gson
import com.sync.android.security.PairedDeviceManager
import com.sync.android.security.PairedHostConfig
import java.io.BufferedReader
import java.io.InputStreamReader
import java.io.OutputStream
import java.net.ServerSocket
import java.net.Socket
import java.net.URLDecoder
import java.util.Random
import java.util.UUID
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

class WatchCompanionServer(private val context: Context, val port: Int = 42424) {

    companion object {
        private const val TAG = "WatchCompanionServer"
        var instance: WatchCompanionServer? = null
        var isWatchConnected: Boolean = false
        var lastWatchIp: String = ""
        var pairingPin: String = String.format("%06d", Random().nextInt(1000000))
        var onWatchStatusChanged: (() -> Unit)? = null
    }

    init {
        instance = this
    }

    private var serverSocket: ServerSocket? = null
    private val isRunning = AtomicBoolean(false)
    private var executor: ExecutorService? = null
    private val gson = Gson()

    fun start(): Boolean {
        if (isRunning.get()) return true
        return try {
            serverSocket = ServerSocket(port)
            isRunning.set(true)
            executor = Executors.newCachedThreadPool()

            executor?.submit {
                Log.d(TAG, "⌚ Watch Companion Sunucusu port $port üzerinde başlatıldı.")
                while (isRunning.get() && serverSocket?.isClosed == false) {
                    try {
                        val client = serverSocket?.accept() ?: break
                        executor?.submit { handleClient(client) }
                    } catch (e: Exception) {
                        if (isRunning.get()) Log.w(TAG, "Watch server accept hatası: ${e.message}")
                    }
                }
            }
            true
        } catch (e: Exception) {
            Log.e(TAG, "Watch companion sunucusu başlatılamadı ($port): ${e.message}")
            false
        }
    }

    fun stop() {
        isRunning.set(false)
        try {
            serverSocket?.close()
            serverSocket = null
            executor?.shutdownNow()
            executor = null
        } catch (e: Exception) {
            Log.w(TAG, "Sunucu kapatma hatası: ${e.message}")
        }
    }

    private fun handleClient(socket: Socket) {
        val clientIp = socket.inetAddress.hostAddress ?: ""
        lastWatchIp = clientIp
        isWatchConnected = true
        onWatchStatusChanged?.invoke()

        try {
            val reader = BufferedReader(InputStreamReader(socket.getInputStream()))
            val out = socket.getOutputStream()

            val requestLine = reader.readLine() ?: return
            val parts = requestLine.split(" ")
            if (parts.size < 2) return

            val method = parts[0]
            val fullPath = parts[1]

            // Parse path and query parameters
            val pathParts = fullPath.split("?", limit = 2)
            val path = pathParts[0]
            val queryMap = mutableMapOf<String, String>()

            if (pathParts.size > 1) {
                val queryParams = pathParts[1].split("&")
                for (param in queryParams) {
                    val kv = param.split("=", limit = 2)
                    if (kv.isNotEmpty()) {
                        val key = URLDecoder.decode(kv[0], "UTF-8")
                        val value = if (kv.size > 1) URLDecoder.decode(kv[1], "UTF-8") else ""
                        queryMap[key] = value
                    }
                }
            }

            // Route request
            when {
                path == "/status" -> handleStatus(out)
                path == "/pair/confirm" -> handlePairConfirm(queryMap, clientIp, out)
                path == "/pair/reset" -> handlePairReset(out)
                path == "/phone/command" -> handlePhoneCommand(queryMap, out)
                path == "/ringer/set" -> handleRingerSet(queryMap, out)
                path == "/call/action" -> handleCallAction(queryMap, out)
                path == "/notification/dismiss" -> handleNotificationDismiss(queryMap, out)
                path == "/notification/clear" -> handleNotificationClear(out)
                else -> sendJsonResponse(out, 404, mapOf("error" to "Not Found"))
            }

            socket.close()
        } catch (e: Exception) {
            Log.e(TAG, "İstek işleme hatası: ${e.message}")
        }
    }

    private fun handleStatus(out: OutputStream) {
        val bm = context.getSystemService(Context.BATTERY_SERVICE) as? BatteryManager
        val batteryLevel = bm?.getIntProperty(BatteryManager.BATTERY_PROPERTY_CAPACITY) ?: 100
        val isCharging = bm?.isCharging == true

        val manager = PairedDeviceManager.getInstance(context)
        val watchDevice = manager.getDevice("apple-watch")
        val isPaired = watchDevice?.isPaired == true

        val media = PhoneController.getCurrentMedia(context)
        val mediaMap = if (media != null) {
            mapOf(
                "source" to "phone",
                "title" to media.title,
                "artist" to media.artist,
                "album" to media.album,
                "is_playing" to media.is_playing,
                "position_ms" to media.position_ms,
                "duration_ms" to media.duration_ms,
                "percent" to media.percent
            )
        } else {
            mapOf("source" to "phone", "title" to "", "artist" to "", "is_playing" to false)
        }

        val resp = mapOf(
            "status" to "OK",
            "connected" to true,
            "device_name" to (Build.MODEL ?: "Android Telefon"),
            "model" to (Build.MODEL ?: "Android"),
            "is_paired" to isPaired,
            "pairing_pin" to pairingPin,
            "paired_device_name" to (watchDevice?.name ?: "Apple Watch"),
            "local_ip" to (socketLocalIp() ?: "127.0.0.1"),
            "port" to port,
            "device_info" to mapOf(
                "device_name" to (Build.MODEL ?: "Android Telefon"),
                "model" to (Build.MODEL ?: "Android"),
                "battery_level" to batteryLevel,
                "is_charging" to isCharging
            ),
            "phone_media" to mediaMap,
            "call_state" to SyncNotificationListenerService.currentCallState,
            "notifications" to SyncNotificationListenerService.recentNotifications.toList()
        )

        sendJsonResponse(out, 200, resp)
    }

    private fun handlePairConfirm(params: Map<String, String>, clientIp: String, out: OutputStream) {
        val pin = params["pin"] ?: ""
        val devName = params["device_name"] ?: "Apple Watch"

        if (pin.isNotEmpty() && pin != pairingPin) {
            sendJsonResponse(out, 401, mapOf("success" to false, "error" to "Geçersiz PIN"))
            return
        }

        val manager = PairedDeviceManager.getInstance(context)
        val paired = PairedHostConfig(
            clientId = "apple-watch",
            name = devName,
            os = "watchOS",
            authToken = UUID.randomUUID().toString(),
            isPaired = true,
            allowClipboard = true,
            allowCalls = true,
            allowSms = true,
            allowMedia = true,
            lastSeen = System.currentTimeMillis(),
            lastIp = clientIp,
            lastPort = port
        )
        manager.saveDevice(paired)
        onWatchStatusChanged?.invoke()

        Log.d(TAG, "⌚ Apple Watch doğrudan telefonla eşleştirildi! (IP: $clientIp, PIN: $pairingPin)")

        val resp = mapOf(
            "success" to true,
            "is_paired" to true,
            "device_name" to devName,
            "pairing_pin" to pairingPin,
            "message" to "Saat doğrudan telefonla eşleştirildi!"
        )
        sendJsonResponse(out, 200, resp)
    }

    private fun handlePairReset(out: OutputStream) {
        pairingPin = String.format("%06d", Random().nextInt(1000000))
        val manager = PairedDeviceManager.getInstance(context)
        manager.unpair("apple-watch")
        onWatchStatusChanged?.invoke()

        sendJsonResponse(out, 200, mapOf("success" to true, "pairing_pin" to pairingPin))
    }

    private fun handlePhoneCommand(params: Map<String, String>, out: OutputStream) {
        val action = params["action"]?.uppercase() ?: ""
        Log.d(TAG, "Telefona doğrudan saat komutu geldi: $action")
        PhoneController.handleAction(context, action)
        sendJsonResponse(out, 200, mapOf("success" to true, "action" to action))
    }

    private fun handleRingerSet(params: Map<String, String>, out: OutputStream) {
        val mode = params["mode"]?.uppercase() ?: "NORMAL"
        val audioManager = context.getSystemService(Context.AUDIO_SERVICE) as? android.media.AudioManager
        when (mode) {
            "SILENT" -> audioManager?.ringerMode = android.media.AudioManager.RINGER_MODE_SILENT
            "VIBRATE" -> audioManager?.ringerMode = android.media.AudioManager.RINGER_MODE_VIBRATE
            "NORMAL" -> audioManager?.ringerMode = android.media.AudioManager.RINGER_MODE_NORMAL
        }
        sendJsonResponse(out, 200, mapOf("success" to true, "mode" to mode))
    }

    private fun handleCallAction(params: Map<String, String>, out: OutputStream) {
        val action = params["action"]?.uppercase() ?: ""
        CallManager.handleCallAction(context, action)
        sendJsonResponse(out, 200, mapOf("success" to true, "call_action" to action))
    }

    private fun handleNotificationDismiss(params: Map<String, String>, out: OutputStream) {
        val id = params["id"] ?: ""
        if (id.isNotEmpty()) {
            val target = SyncNotificationListenerService.recentNotifications.firstOrNull { it["id"] == id }
            SyncNotificationListenerService.recentNotifications.removeIf { it["id"] == id }
            val key = target?.get("key")?.toString() ?: ""
            if (key.isNotEmpty()) {
                SyncNotificationListenerService.dismissNotification(key)
            }
        }
        sendJsonResponse(out, 200, mapOf("success" to true))
    }

    private fun handleNotificationClear(out: OutputStream) {
        SyncNotificationListenerService.recentNotifications.clear()
        sendJsonResponse(out, 200, mapOf("success" to true))
    }

    private fun sendJsonResponse(out: OutputStream, statusCode: Int, data: Any) {
        val statusText = if (statusCode == 200) "OK" else if (statusCode == 401) "Unauthorized" else "Error"
        val body = gson.toJson(data)
        val bodyBytes = body.toByteArray(Charsets.UTF_8)

        val header = "HTTP/1.1 $statusCode $statusText\r\n" +
                "Content-Type: application/json; charset=utf-8\r\n" +
                "Access-Control-Allow-Origin: *\r\n" +
                "Content-Length: ${bodyBytes.size}\r\n" +
                "Connection: close\r\n\r\n"

        out.write(header.toByteArray(Charsets.UTF_8))
        out.write(bodyBytes)
        out.flush()
    }

    private fun socketLocalIp(): String? {
        return try {
            val interfaces = java.util.Collections.list(java.net.NetworkInterface.getNetworkInterfaces())
            for (intf in interfaces) {
                val addrs = java.util.Collections.list(intf.inetAddresses)
                for (addr in addrs) {
                    if (!addr.isLoopbackAddress && addr is java.net.Inet4Address) {
                        return addr.hostAddress
                    }
                }
            }
            null
        } catch (_: Exception) {
            null
        }
    }
}
