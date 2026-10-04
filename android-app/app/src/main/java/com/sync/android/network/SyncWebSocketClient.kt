package com.sync.android.network

import android.content.Context
import android.os.BatteryManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.util.Log
import com.google.gson.Gson
import com.sync.android.model.*
import com.sync.android.security.PairedDeviceManager
import com.sync.android.security.PairedHostConfig
import okhttp3.*
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.TimeUnit

data class ConnectedHost(
    val key: String, // "ip:port"
    val ip: String,
    val port: Int,
    var name: String,
    var clientId: String = "",
    var os: String = "windows",
    var webSocket: WebSocket? = null,
    var isConnected: Boolean = false,
    var isConnecting: Boolean = false,
    var isAuthorized: Boolean = false,
    var pairingPin: String = "",
    var lastMedia: MediaInfoPayload? = null
)

class SyncWebSocketClient(
    private val context: Context,
    private val onConnectionsChanged: (connectedCount: Int, hostNames: List<String>) -> Unit,
    private val onClipboardReceived: (payload: ClipboardPayload, fromHostKey: String) -> Unit
) {
    private val TAG = "SyncWebSocketClient"
    private val okHttpClient = OkHttpClient.Builder()
        .pingInterval(8, TimeUnit.SECONDS)
        .connectTimeout(5, TimeUnit.SECONDS)
        .readTimeout(0, TimeUnit.MILLISECONDS)
        .retryOnConnectionFailure(true)
        .build()

    // Host Connection Pool (Supports multiple PCs simultaneously)
    private val hosts = ConcurrentHashMap<String, ConnectedHost>()
    private val mainHandler = Handler(Looper.getMainLooper())

    var onPhoneCommandReceived: ((action: String, fromHostKey: String) -> Unit)? = null
    var onMediaInfoReceived: ((info: MediaInfoPayload, hostName: String, fromHostKey: String) -> Unit)? = null
    var onCallActionReceived: ((action: String, number: String?, value: Boolean?, fromHostKey: String) -> Unit)? = null
    var onSmsSyncRequested: ((fromHostKey: String) -> Unit)? = null
    var onSmsSendRequested: ((recipient: String, body: String, fromHostKey: String) -> Unit)? = null
    var onPairingRequested: ((host: ConnectedHost, pin: String) -> Unit)? = null
    var onPairingConfirmed: ((host: ConnectedHost) -> Unit)? = null
    var onPCNotificationReceived: ((notif: NotificationPayload, hostKey: String, hostName: String) -> Unit)? = null
    var onFileAvailableReceived: ((payload: FileAvailablePayload, hostKey: String) -> Unit)? = null
    var onNotificationReplyReceived: ((payload: NotificationReplyPayload, hostKey: String) -> Unit)? = null
    var onNotificationActionReceived: ((payload: NotificationActionPayload, hostKey: String) -> Unit)? = null
    var onNotificationDismissReceived: ((notificationKey: String, hostKey: String) -> Unit)? = null
    var onOpenUrlReceived: ((url: String, hostKey: String) -> Unit)? = null
    var onContactsRequestReceived: ((hostKey: String) -> Unit)? = null
    var onPhotosRequestReceived: ((hostKey: String) -> Unit)? = null
    var onPhotoDownloadRequestReceived: ((photoId: Long, uploadUrl: String?, hostKey: String) -> Unit)? = null

    val isConnected: Boolean
        get() = hosts.values.any { it.isConnected }

    val connectedCount: Int
        get() = hosts.values.count { it.isConnected }

    val authorizedCount: Int
        get() = hosts.values.count { it.isConnected && it.isAuthorized }

    val connectedHosts: List<ConnectedHost>
        get() = hosts.values.toList()

    val connectedNames: List<String>
        get() = hosts.values.filter { it.isConnected }.map { it.name.ifEmpty { it.ip } }

    @Synchronized
    fun connect(ip: String, port: Int, serverName: String = "Bilgisayar") {
        val key = "$ip:$port"
        val existing = hosts[key]
        if (existing != null) {
            if (existing.isConnected || existing.isConnecting) {
                if (serverName.isNotEmpty() && serverName != "Bilgisayar") {
                    existing.name = serverName
                }
                return
            }
        }

        val detectedOs = if (serverName.lowercase().contains("mac")) "mac" else "windows"
        val host = existing ?: ConnectedHost(
            key = key,
            ip = ip,
            port = port,
            name = if (serverName.isNotEmpty()) serverName else "Bilgisayar ($ip)",
            os = detectedOs
        )
        host.name = if (serverName.isNotEmpty()) serverName else host.name
        host.os = detectedOs
        host.isConnecting = true
        hosts[key] = host

        val url = "ws://$ip:$port/ws"
        Log.d(TAG, "[$key] Çoklu bağlantı başlatılıyor: $url ($serverName)")

        val request = Request.Builder().url(url).build()
        val ws = okHttpClient.newWebSocket(request, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                Log.d(TAG, "[$key] Bağlantı soketi açıldı, güvenlik el sıkışması bekleniyor...")
                host.webSocket = webSocket
                host.isConnected = true
                host.isConnecting = false

                notifyConnectionChange()
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                handleInboundMessage(key, text)
            }

            override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                webSocket.close(1000, null)
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                Log.d(TAG, "[$key] Bağlantı kapandı: $reason")
                handleDisconnect(key)
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                Log.e(TAG, "[$key] Bağlantı hatası: ${t.message}")
                handleDisconnect(key)
            }
        })
        host.webSocket = ws
    }

    private fun handleDisconnect(key: String) {
        val host = hosts[key] ?: return
        host.isConnected = false
        host.isConnecting = false
        host.isAuthorized = false
        host.webSocket = null
        notifyConnectionChange()

        // Schedule auto-reconnect after 4 seconds
        mainHandler.postDelayed({
            val h = hosts[key]
            if (h != null && !h.isConnected && !h.isConnecting) {
                Log.d(TAG, "[$key] Yeniden bağlanma deneniyor...")
                connect(h.ip, h.port, h.name)
            }
        }, 4000)
    }

    private fun notifyConnectionChange() {
        mainHandler.post {
            onConnectionsChanged(connectedCount, connectedNames)
        }
    }

    private fun handleInboundMessage(hostKey: String, json: String) {
        try {
            val msg = Gson().fromJson(json, SyncMessage::class.java)
            val host = hosts[hostKey]
            val hostName = host?.name ?: hostKey
            val manager = PairedDeviceManager.getInstance(context)

            when (msg.event) {
                ProtocolEvents.AUTH_REQUEST -> {
                    val authReq = Gson().fromJson(msg.payload, AuthRequestPayload::class.java)
                    if (host != null) {
                        host.clientId = authReq.client_id
                        host.os = authReq.os
                        if (authReq.client_name.isNotEmpty()) {
                            host.name = authReq.client_name
                        }
                        host.pairingPin = authReq.pairing_pin
                    }

                    val isAuth = manager.isAuthorized(authReq.client_id, authReq.auth_token)
                    if (isAuth) {
                        host?.isAuthorized = true
                        val resp = AuthResponsePayload(status = "AUTHORIZED", client_name = Build.MODEL)
                        host?.webSocket?.send(resp.toSyncMessage(ProtocolEvents.AUTH_RESPONSE))
                        sendDeviceInfoTo(host?.webSocket ?: return)
                        notifyConnectionChange()
                        Log.d(TAG, "[$hostName] Cihaz önceden eşleşmiş, yetkilendirildi 🟢")
                    } else {
                        host?.isAuthorized = false
                        val resp = AuthResponsePayload(status = "PAIRING_REQUIRED", client_name = Build.MODEL)
                        host?.webSocket?.send(resp.toSyncMessage(ProtocolEvents.AUTH_RESPONSE))
                        notifyConnectionChange()
                        Log.w(TAG, "[$hostName] Eşleştirme gerekiyor! Kod: ${authReq.pairing_pin}")
                        if (host != null) {
                            mainHandler.post {
                                onPairingRequested?.invoke(host, authReq.pairing_pin)
                            }
                        }
                    }
                }

                ProtocolEvents.UNPAIR -> {
                    if (host != null) {
                        manager.unpair(host.clientId)
                        host.isAuthorized = false
                        notifyConnectionChange()
                        Log.d(TAG, "[$hostName] Bilgisayar tarafından eşleşme kaldırıldı.")
                    }
                }

                ProtocolEvents.DEVICE_INFO -> {
                    try {
                        val map = Gson().fromJson(msg.payload, Map::class.java)
                        val sName = map["server_name"] as? String
                        if (!sName.isNullOrEmpty() && host != null) {
                            host.name = sName
                            notifyConnectionChange()
                        }
                    } catch (e: Exception) {}
                }

                ProtocolEvents.CLIPBOARD -> {
                    val config = host?.clientId?.let { manager.getDevice(it) }
                    if (host?.isAuthorized == true && (config == null || config.allowClipboard)) {
                        val clip = Gson().fromJson(msg.payload, ClipboardPayload::class.java)
                        Log.d(TAG, "[$hostName] Panodan içerik alındı (tip=${clip.type}, metin_uzunluğu=${clip.text.length}, görsel=${clip.image_base64 != null})")
                        onClipboardReceived(clip, hostKey)
                    } else {
                        Log.w(TAG, "[$hostName] Pano izni kapalı veya yetkisiz cihaz, gözardı edildi.")
                    }
                }

                ProtocolEvents.PHONE_COMMAND, ProtocolEvents.MEDIA_COMMAND -> {
                    val config = host?.clientId?.let { manager.getDevice(it) }
                    if (host?.isAuthorized == true && (config == null || config.allowMedia)) {
                        val cmd = Gson().fromJson(msg.payload, PhoneCommandPayload::class.java)
                        val action = if (cmd.action.startsWith("SEEK_PERCENT") || cmd.action == "SEEK") {
                            "SEEK_PERCENT:${cmd.percent}"
                        } else {
                            cmd.action
                        }
                        Log.d(TAG, "[$hostName] Komut alındı: $action (percent: ${cmd.percent})")
                        onPhoneCommandReceived?.invoke(action, hostKey)
                    }
                }

                ProtocolEvents.MEDIA_INFO -> {
                    val info = Gson().fromJson(msg.payload, MediaInfoPayload::class.java)
                    host?.lastMedia = info
                    onMediaInfoReceived?.invoke(info, hostName, hostKey)
                }

                ProtocolEvents.CALL_ACTION -> {
                    val config = host?.clientId?.let { manager.getDevice(it) }
                    if (host?.isAuthorized == true && (config == null || config.allowCalls)) {
                        val callAct = Gson().fromJson(msg.payload, CallActionPayload::class.java)
                        Log.d(TAG, "[$hostName] Çağrı aksiyonu onaylandı: ${callAct.action}")
                        onCallActionReceived?.invoke(callAct.action, callAct.number, callAct.value, hostKey)
                    } else {
                        Log.w(TAG, "[$hostName] Arama izni kapalı, çağrı aksiyonu reddedildi.")
                    }
                }

                ProtocolEvents.SMS_SYNC_REQUEST -> {
                    val config = host?.clientId?.let { manager.getDevice(it) }
                    if (host?.isAuthorized == true && (config == null || config.allowSms)) {
                        Log.d(TAG, "[$hostName] SMS senkronizasyon isteği kabul edildi")
                        onSmsSyncRequested?.invoke(hostKey)
                    } else {
                        Log.w(TAG, "[$hostName] SMS izni kapalı, senkronizasyon reddedildi.")
                    }
                }

                ProtocolEvents.SMS_SEND -> {
                    val config = host?.clientId?.let { manager.getDevice(it) }
                    if (host?.isAuthorized == true && (config == null || config.allowSms)) {
                        val smsSend = Gson().fromJson(msg.payload, SmsSendPayload::class.java)
                        Log.d(TAG, "[$hostName] SMS gönderim isteği kabul edildi: ${smsSend.recipient}")
                        onSmsSendRequested?.invoke(smsSend.recipient, smsSend.body, hostKey)
                    } else {
                        Log.w(TAG, "[$hostName] SMS izni kapalı, SMS gönderimi reddedildi.")
                    }
                }

                ProtocolEvents.PC_NOTIFICATION, ProtocolEvents.NOTIFICATION -> {
                    if (host?.isAuthorized == true) {
                        try {
                            val notif = Gson().fromJson(msg.payload, NotificationPayload::class.java)
                            Log.d(TAG, "[$hostName] PC bildirimi alındı: [${notif.app_name}] ${notif.title}: ${notif.text}")
                            onPCNotificationReceived?.invoke(notif, hostKey, hostName)
                        } catch (e: Exception) {
                            Log.e(TAG, "PC bildirimi parse hatası: ${e.message}")
                        }
                    }
                }

                ProtocolEvents.FILE_AVAILABLE -> {
                    if (host?.isAuthorized == true) {
                        try {
                            val filePayload = Gson().fromJson(msg.payload, FileAvailablePayload::class.java)
                            Log.d(TAG, "[$hostName] Dosya indirme bildirimi alındı: ${filePayload.file_name} (${filePayload.file_size} B)")
                            onFileAvailableReceived?.invoke(filePayload, hostKey)
                        } catch (e: Exception) {
                            Log.e(TAG, "FILE_AVAILABLE parse hatası: ${e.message}")
                        }
                    }
                }

                ProtocolEvents.NOTIFICATION_REPLY -> {
                    if (host?.isAuthorized == true) {
                        try {
                            val replyPayload = Gson().fromJson(msg.payload, NotificationReplyPayload::class.java)
                            Log.d(TAG, "[$hostName] Bildirime yanıt isteği alındı: key=${replyPayload.notification_key}, text=${replyPayload.reply_text}")
                            onNotificationReplyReceived?.invoke(replyPayload, hostKey)
                        } catch (e: Exception) {
                            Log.e(TAG, "NOTIFICATION_REPLY parse hatası: ${e.message}")
                        }
                    }
                }

                ProtocolEvents.NOTIFICATION_ACTION -> {
                    if (host?.isAuthorized == true) {
                        try {
                            val actionPayload = Gson().fromJson(msg.payload, NotificationActionPayload::class.java)
                            Log.d(TAG, "[$hostName] Bildirim aksiyon isteği alındı: key=${actionPayload.notification_key}, index=${actionPayload.action_index}")
                            onNotificationActionReceived?.invoke(actionPayload, hostKey)
                        } catch (e: Exception) {
                            Log.e(TAG, "NOTIFICATION_ACTION parse hatası: ${e.message}")
                        }
                    }
                }

                ProtocolEvents.NOTIFICATION_DISMISS -> {
                    if (host?.isAuthorized == true) {
                        try {
                            val dismissPayload = Gson().fromJson(msg.payload, NotificationDismissPayload::class.java)
                            Log.d(TAG, "[$hostName] Bildirim kapatma isteği alındı: key=${dismissPayload.notification_key}")
                            onNotificationDismissReceived?.invoke(dismissPayload.notification_key, hostKey)
                        } catch (e: Exception) {
                            Log.e(TAG, "NOTIFICATION_DISMISS parse hatası: ${e.message}")
                        }
                    }
                }

                ProtocolEvents.OPEN_URL -> {
                    if (host?.isAuthorized == true) {
                        try {
                            val openUrlPayload = Gson().fromJson(msg.payload, OpenUrlPayload::class.java)
                            Log.d(TAG, "[$hostName] URL açma isteği alındı: ${openUrlPayload.url}")
                            onOpenUrlReceived?.invoke(openUrlPayload.url, hostKey)
                        } catch (e: Exception) {
                            Log.e(TAG, "OPEN_URL parse hatası: ${e.message}")
                        }
                    }
                }

                ProtocolEvents.CONTACTS_REQUEST -> {
                    val config = host?.clientId?.let { manager.getDevice(it) }
                    if (host?.isAuthorized == true && (config == null || config.allowCalls || config.allowSms)) {
                        Log.d(TAG, "[$hostName] Rehber senkronizasyon isteği kabul edildi")
                        onContactsRequestReceived?.invoke(hostKey)
                    } else {
                        Log.w(TAG, "[$hostName] İzin kapalı veya yetkisiz, rehber senkronizasyonu reddedildi.")
                    }
                }

                ProtocolEvents.PHOTOS_REQUEST -> {
                    if (host?.isAuthorized == true) {
                        Log.d(TAG, "[$hostName] Fotoğraf galerisi senkronizasyon isteği kabul edildi")
                        onPhotosRequestReceived?.invoke(hostKey)
                    } else {
                        Log.w(TAG, "[$hostName] Yetkisiz cihaz, fotoğraf isteği reddedildi.")
                    }
                }

                ProtocolEvents.PHOTO_DOWNLOAD_REQUEST -> {
                    if (host?.isAuthorized == true) {
                        try {
                            val p = Gson().fromJson(msg.payload, PhotoDownloadRequestPayload::class.java)
                            Log.d(TAG, "[$hostName] Fotoğraf indirme isteği alındı: id=${p.id}")
                            onPhotoDownloadRequestReceived?.invoke(p.id, p.upload_url, hostKey)
                        } catch (e: Exception) {
                            Log.e(TAG, "PHOTO_DOWNLOAD_REQUEST parse hatası: ${e.message}")
                        }
                    }
                }

                ProtocolEvents.PONG -> {}
            }
        } catch (e: Exception) {
            Log.e(TAG, "Mesaj işleme hatası: ${e.message}")
        }
    }

    fun approvePairing(hostKey: String) {
        val host = hosts[hostKey] ?: return
        val manager = PairedDeviceManager.getInstance(context)
        val token = manager.generateAuthToken()
        val cId = host.clientId.ifEmpty { "client-${host.ip.replace(".", "-")}" }

        val pairedConfig = PairedHostConfig(
            clientId = cId,
            name = host.name,
            os = host.os,
            authToken = token,
            isPaired = true,
            allowClipboard = true,
            allowCalls = true,
            allowSms = true,
            allowMedia = true,
            lastIp = host.ip,
            lastPort = host.port
        )
        manager.saveDevice(pairedConfig)
        host.clientId = cId
        host.isAuthorized = true

        val confirm = PairConfirmPayload(
            approved = true,
            auth_token = token,
            device_name = Build.MODEL,
            client_id = cId
        )
        host.webSocket?.send(confirm.toSyncMessage(ProtocolEvents.PAIR_CONFIRM))
        sendDeviceInfoTo(host.webSocket ?: return)
        notifyConnectionChange()
        onPairingConfirmed?.invoke(host)
        Log.d(TAG, "[$hostKey] Cihaz eşleştirmesi kullanıcı tarafından onaylandı ✅")
    }

    fun rejectPairing(hostKey: String) {
        val host = hosts[hostKey] ?: return
        val confirm = PairConfirmPayload(
            approved = false,
            client_id = host.clientId
        )
        host.webSocket?.send(confirm.toSyncMessage(ProtocolEvents.PAIR_CONFIRM))
        host.webSocket?.close(1000, "Pairing rejected")
        host.isConnected = false
        host.isAuthorized = false
        notifyConnectionChange()
        Log.d(TAG, "[$hostKey] Cihaz eşleştirmesi kullanıcı tarafından reddedildi ❌")
    }

    fun unpairHost(clientId: String) {
        val manager = PairedDeviceManager.getInstance(context)
        manager.unpair(clientId)
        for (host in hosts.values) {
            if (host.clientId == clientId || host.key == clientId) {
                host.isAuthorized = false
                val unpairMsg = mapOf("client_id" to host.clientId)
                host.webSocket?.send(Gson().toJson(SyncMessage(ProtocolEvents.UNPAIR, Gson().toJsonTree(unpairMsg))))
                host.webSocket?.close(1000, "Unpaired")
                host.isConnected = false
            }
        }
        notifyConnectionChange()
    }

    private fun createDeviceInfoPayload(): DeviceInfoPayload {
        val bm = context.getSystemService(Context.BATTERY_SERVICE) as? BatteryManager
        val level = bm?.getIntProperty(BatteryManager.BATTERY_PROPERTY_CAPACITY) ?: -1
        val isCharging = bm?.isCharging ?: false

        return DeviceInfoPayload(
            device_name = Build.DEVICE,
            model = Build.MODEL,
            battery_level = level,
            is_charging = isCharging
        )
    }

    private fun sendDeviceInfoTo(ws: WebSocket) {
        val payload = createDeviceInfoPayload()
        ws.send(payload.toSyncMessage(ProtocolEvents.DEVICE_INFO))
    }

    fun sendDeviceInfo() {
        val payload = createDeviceInfoPayload()
        broadcastMessage(payload.toSyncMessage(ProtocolEvents.DEVICE_INFO))
    }

    fun sendNotification(
        id: String,
        pkg: String,
        appName: String,
        title: String,
        text: String,
        key: String = "",
        canReply: Boolean = false,
        actions: List<NotificationActionItem>? = null
    ) {
        val payload = NotificationPayload(
            id = id,
            package_name = pkg,
            app_name = appName,
            title = title,
            text = text,
            timestamp = System.currentTimeMillis(),
            key = key,
            can_reply = canReply,
            actions = actions
        )
        // Sadece yetkili bilgisayarlara bildirim gönder
        for (host in hosts.values) {
            if (host.isConnected && host.isAuthorized) {
                host.webSocket?.send(payload.toSyncMessage(ProtocolEvents.NOTIFICATION))
            }
        }
    }

    fun sendNotificationDismiss(key: String, id: String? = null) {
        val payload = NotificationDismissPayload(
            notification_key = key,
            notification_id = id
        )
        val json = payload.toSyncMessage(ProtocolEvents.NOTIFICATION_DISMISS)
        for (host in hosts.values) {
            if (host.isConnected && host.isAuthorized) {
                host.webSocket?.send(json)
            }
        }
    }

    fun sendCallState(state: String, number: String, callerName: String = "") {
        val manager = PairedDeviceManager.getInstance(context)
        val payload = CallStatePayload(
            state = state,
            phone_number = number,
            caller_name = callerName
        )
        val json = payload.toSyncMessage(ProtocolEvents.CALL_STATE)

        for (host in hosts.values) {
            if (!host.isConnected || !host.isAuthorized) continue
            val config = manager.getDevice(host.clientId)
            if (config == null || config.allowCalls) {
                host.webSocket?.send(json)
            }
        }
    }

    fun sendMediaCommand(action: String, targetHostKey: String? = null) {
        val payload = MediaCommandPayload(action = action)
        val json = payload.toSyncMessage(ProtocolEvents.MEDIA_COMMAND)
        if (targetHostKey != null) {
            sendMessageTo(targetHostKey, json)
        } else {
            broadcastMessage(json)
        }
    }

    fun sendMediaSeekPercent(percent: Double, targetHostKey: String? = null) {
        val payload = MediaCommandPayload(action = "SEEK_PERCENT", percent = percent)
        val json = payload.toSyncMessage(ProtocolEvents.MEDIA_COMMAND)
        if (targetHostKey != null) {
            sendMessageTo(targetHostKey, json)
        } else {
            broadcastMessage(json)
        }
    }

    fun sendMediaInfo(info: MediaInfoPayload) {
        broadcastMessage(info.toSyncMessage(ProtocolEvents.MEDIA_INFO))
    }

    fun sendTouchpadEvent(payload: TouchpadEventPayload, targetHostKey: String? = null) {
        val json = payload.toSyncMessage(ProtocolEvents.TOUCHPAD_EVENT)
        if (targetHostKey != null) {
            sendMessageTo(targetHostKey, json)
        } else {
            broadcastMessage(json)
        }
    }

    fun sendClipboard(payload: ClipboardPayload, excludeHostKey: String? = null) {
        val manager = PairedDeviceManager.getInstance(context)
        val json = payload.toSyncMessage(ProtocolEvents.CLIPBOARD)

        for ((key, host) in hosts) {
            if (excludeHostKey != null && key == excludeHostKey) continue
            if (!host.isConnected || !host.isAuthorized) continue
            val config = manager.getDevice(host.clientId)
            if (config == null || config.allowClipboard) {
                host.webSocket?.send(json)
            }
        }
    }

    fun sendClipboard(text: String, excludeHostKey: String? = null) {
        sendClipboard(
            ClipboardPayload(
                text = text,
                type = "text",
                timestamp = System.currentTimeMillis()
            ),
            excludeHostKey
        )
    }

    fun sendClipboardImage(imageBase64: String, mimeType: String = "image/png", excludeHostKey: String? = null) {
        sendClipboard(
            ClipboardPayload(
                type = "image",
                image_base64 = imageBase64,
                mime_type = mimeType,
                timestamp = System.currentTimeMillis()
            ),
            excludeHostKey
        )
    }

    fun sendSmsSyncResponse(messages: List<SmsMessage>, targetHostKey: String? = null) {
        val payload = SmsSyncPayload(messages = messages)
        val json = payload.toSyncMessage(ProtocolEvents.SMS_SYNC_RESPONSE)
        if (targetHostKey != null) {
            sendMessageTo(targetHostKey, json)
        } else {
            val manager = PairedDeviceManager.getInstance(context)
            for (host in hosts.values) {
                if (!host.isConnected || !host.isAuthorized) continue
                val config = manager.getDevice(host.clientId)
                if (config == null || config.allowSms) {
                    host.webSocket?.send(json)
                }
            }
        }
    }

    fun sendSmsSentStatus(success: Boolean, recipient: String, body: String, error: String? = null) {
        val payload = SmsSentStatusPayload(success, recipient, body, error)
        broadcastMessage(payload.toSyncMessage(ProtocolEvents.SMS_SENT_STATUS))
    }

    fun sendMediaCommand(action: String, percent: Double = 0.0, targetHostKey: String? = null) {
        val payload = MediaCommandPayload(action = action, percent = percent)
        val json = payload.toSyncMessage(ProtocolEvents.MEDIA_COMMAND)
        if (targetHostKey != null) {
            sendMessageTo(targetHostKey, json)
        } else {
            val manager = PairedDeviceManager.getInstance(context)
            for (host in hosts.values) {
                if (!host.isConnected || !host.isAuthorized) continue
                val config = manager.getDevice(host.clientId)
                if (config == null || config.allowMedia) {
                    host.webSocket?.send(json)
                }
            }
        }
    }

    fun sendNewSmsMessage(msg: SmsMessage) {
        val manager = PairedDeviceManager.getInstance(context)
        val json = msg.toSyncMessage(ProtocolEvents.SMS_NEW_MESSAGE)
        for (host in hosts.values) {
            if (!host.isConnected || !host.isAuthorized) continue
            val config = manager.getDevice(host.clientId)
            if (config == null || config.allowSms) {
                host.webSocket?.send(json)
            }
        }
    }

    fun sendPCNotification(notif: NotificationPayload, excludeHostKey: String? = null) {
        val json = notif.toSyncMessage(ProtocolEvents.PC_NOTIFICATION)
        for ((key, host) in hosts) {
            if (excludeHostKey != null && key == excludeHostKey) continue
            if (host.isConnected && host.isAuthorized) {
                host.webSocket?.send(json)
            }
        }
    }

    fun broadcastMessage(json: String, excludeHostKey: String? = null) {
        for ((key, host) in hosts) {
            if (excludeHostKey != null && key == excludeHostKey) continue
            if (host.isConnected && host.isAuthorized) {
                host.webSocket?.send(json)
            }
        }
    }

    fun sendMessageTo(hostKey: String, json: String) {
        val host = hosts[hostKey]
        if (host != null && host.isConnected) {
            host.webSocket?.send(json)
        }
    }

    fun sendOpenUrl(url: String, targetHostKey: String? = null) {
        val payload = OpenUrlPayload(url = url, sender = Build.MODEL)
        val json = payload.toSyncMessage(ProtocolEvents.OPEN_URL)
        if (targetHostKey != null) {
            sendMessageTo(targetHostKey, json)
        } else {
            broadcastMessage(json)
        }
    }

    fun sendContactsResponse(contacts: List<ContactItem>, targetHostKey: String? = null) {
        val payload = ContactsResponsePayload(contacts = contacts)
        val json = payload.toSyncMessage(ProtocolEvents.CONTACTS_RESPONSE)
        if (targetHostKey != null) {
            sendMessageTo(targetHostKey, json)
        } else {
            broadcastMessage(json)
        }
    }

    fun sendPhotosResponse(photos: List<PhotoItem>, targetHostKey: String? = null) {
        val payload = PhotosResponsePayload(photos = photos, count = photos.size)
        val json = payload.toSyncMessage(ProtocolEvents.PHOTOS_RESPONSE)
        if (targetHostKey != null) {
            sendMessageTo(targetHostKey, json)
        } else {
            broadcastMessage(json)
        }
    }

    fun disconnect() {
        mainHandler.removeCallbacksAndMessages(null)
        for (host in hosts.values) {
            host.isConnected = false
            host.isConnecting = false
            host.isAuthorized = false
            host.webSocket?.close(1000, "App disconnected")
            host.webSocket = null
        }
        hosts.clear()
        notifyConnectionChange()
    }
}
