package com.sync.android.service

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import android.telephony.PhoneStateListener
import android.telephony.TelephonyCallback
import android.telephony.TelephonyManager
import android.util.Log
import androidx.core.app.NotificationCompat
import com.sync.android.network.DiscoveryClient
import com.sync.android.network.SyncWebSocketClient

import android.app.PendingIntent
import com.sync.android.MainActivity
import com.sync.android.R

class SyncForegroundService : Service() {

    companion object {
        const val CHANNEL_ID = "mac_sync_channel"
        const val CHANNEL_PC_NOTIF_ID = "channel_pc_notifications"
        const val NOTIF_ID = 1001
        const val ACTION_RESCAN = "com.sync.android.ACTION_RESCAN"
        var instance: SyncForegroundService? = null
    }

    private val TAG = "SyncForegroundService"
    var webSocketClient: SyncWebSocketClient? = null
    var discoveryClient: DiscoveryClient? = null
    private var clipboardManager: ClipboardManager? = null
    private var lastLocalClipboard: String = ""
    private var wakeLock: android.os.PowerManager.WakeLock? = null
    private var wifiLock: android.net.wifi.WifiManager.WifiLock? = null

    var onStatusChanged: ((Boolean, String) -> Unit)? = null
    var onClipboardUpdate: ((String) -> Unit)? = null
    var onMacMediaInfoUpdate: ((com.sync.android.model.MediaInfoPayload) -> Unit)? = null
    var onPairingRequested: ((com.sync.android.network.ConnectedHost, String) -> Unit)? = null
    var lastMacMedia: com.sync.android.model.MediaInfoPayload? = null
    var connectedServerName: String = "Cihaz Aranıyor 🟡"


    private val mediaReporterHandler = android.os.Handler(android.os.Looper.getMainLooper())
    private val mediaReporterRunnable = object : Runnable {
        override fun run() {
            if (webSocketClient?.isConnected == true) {
                val phoneMedia = PhoneController.getCurrentMedia(this@SyncForegroundService)
                if (phoneMedia != null) {
                    webSocketClient?.sendMediaInfo(phoneMedia)
                }
            }
            mediaReporterHandler.postDelayed(this, 1500)
        }
    }

    override fun onCreate() {
        super.onCreate()
        instance = this
        createNotificationChannel()

        // Acquire WakeLock and WifiLock so network never sleeps during calls or screen-off
        try {
            val pm = getSystemService(Context.POWER_SERVICE) as? android.os.PowerManager
            wakeLock = pm?.newWakeLock(android.os.PowerManager.PARTIAL_WAKE_LOCK, "MacSync:WakeLock")?.apply {
                setReferenceCounted(false)
                acquire(24 * 60 * 60 * 1000L) // 24 hours max
            }
            val wm = applicationContext.getSystemService(Context.WIFI_SERVICE) as? android.net.wifi.WifiManager
            wifiLock = wm?.createWifiLock(android.net.wifi.WifiManager.WIFI_MODE_FULL_HIGH_PERF, "MacSync:WifiLock")?.apply {
                setReferenceCounted(false)
                acquire()
            }
            Log.d(TAG, "WakeLock ve WifiLock aktif edildi.")
        } catch (e: Exception) {
            Log.w(TAG, "Lock alma hatası: ${e.message}")
        }

        // Start Foreground with connectedDevice type
        val notification = createServiceNotification("Bilgisayarlar aranıyor...")
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            startForeground(NOTIF_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_CONNECTED_DEVICE)
        } else {
            startForeground(NOTIF_ID, notification)
        }

        initNetworking()
        initClipboard()
        initCallStateListener()
        initSmsSync()
        mediaReporterHandler.post(mediaReporterRunnable)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_RESCAN) {
            restartDiscovery()
        }
        return START_STICKY
    }

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                "Device Sync Servisi",
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "Android ile bilgisayarlarınız arasındaki yerel çoklu senkronizasyon servisi"
            }

            val pcChannel = NotificationChannel(
                CHANNEL_PC_NOTIF_ID,
                "Bilgisayar Bildirimleri (PC & Mac)",
                NotificationManager.IMPORTANCE_HIGH
            ).apply {
                description = "Bağlı bilgisayarlardan telefona iletilen anlık bildirimler"
                enableVibration(true)
                enableLights(true)
            }

            val nm = getSystemService(NotificationManager::class.java)
            nm.createNotificationChannel(channel)
            nm.createNotificationChannel(pcChannel)
        }
    }

    private fun createServiceNotification(status: String): Notification {
        val clickIntent = Intent(this, MainActivity::class.java).apply {
            this.flags = Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP
        }
        val pendingClick = PendingIntent.getActivity(
            this,
            0,
            clickIntent,
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )

        val rescanIntent = Intent(this, SyncForegroundService::class.java).apply {
            action = ACTION_RESCAN
        }
        val pendingRescan = PendingIntent.getService(
            this,
            1,
            rescanIntent,
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )

        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setContentTitle("Android Sync • Masaüstü Ekosistemi")
            .setContentText(status)
            .setSubText("Mesh Aktif")
            .setSmallIcon(android.R.drawable.stat_sys_data_bluetooth)
            .setContentIntent(pendingClick)
            .addAction(android.R.drawable.ic_menu_rotate, "Ağı Yeniden Tara", pendingRescan)
            .setColor(0xFF38BDF8.toInt())
            .setOngoing(true)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .build()
    }

    fun sendMediaCommand(action: String, percent: Double = 0.0, targetHostKey: String? = null) {
        webSocketClient?.sendMediaCommand(action, percent, targetHostKey)
    }

    private fun initNetworking() {
        webSocketClient = SyncWebSocketClient(
            context = this,
            onConnectionsChanged = { count, names ->
                val statusText = when {
                    count == 0 -> "Cihaz Aranıyor 🟡"
                    count == 1 -> "${names[0]} Bağlandı 🟢"
                    else -> "$count Bilgisayara Bağlandı 🟢 (${names.joinToString(", ")})"
                }
                connectedServerName = statusText
                updateNotification(statusText)
                onStatusChanged?.invoke(count > 0, statusText)
            },
            onClipboardReceived = { text, fromHostKey ->
                if (text != lastLocalClipboard) {
                    lastLocalClipboard = text
                    clipboardManager?.setPrimaryClip(ClipData.newPlainText("Device Sync", text))
                    onClipboardUpdate?.invoke(text)
                    Log.d(TAG, "Pano güncellendi: ${text.take(30)}")

                    // Evrensel Pano Mesh İletimi: Metni diğer bağlı bilgisayarlara da ilet (Eğer ayar açıksa)
                    if (com.sync.android.security.PairedDeviceManager.getInstance(this@SyncForegroundService).meshClipboardEnabled) {
                        webSocketClient?.sendClipboard(text, excludeHostKey = fromHostKey)
                    }
                }
            }
        ).apply {
            onPairingRequested = { host, pin ->
                this@SyncForegroundService.onPairingRequested?.invoke(host, pin)
            }
            onPhoneCommandReceived = { action, _ ->
                PhoneController.handleAction(this@SyncForegroundService, action)
            }
            onMediaInfoReceived = { info, _, _ ->
                lastMacMedia = info
                onMacMediaInfoUpdate?.invoke(info)
            }
            onCallActionReceived = { action, number, value, _ ->
                CallManager.handleCallAction(this@SyncForegroundService, action, number, value)
            }
            onSmsSyncRequested = { hostKey ->
                val msgs = SmsSyncManager.fetchRecentMessages(this@SyncForegroundService, 100)
                webSocketClient?.sendSmsSyncResponse(msgs, targetHostKey = hostKey)
            }
            onSmsSendRequested = { recipient, body, _ ->
                val (success, error) = SmsSyncManager.sendSms(this@SyncForegroundService, recipient, body)
                webSocketClient?.sendSmsSentStatus(success, recipient, body, error)
                // Ayrıca güncel mesajları tüm bilgisayarlara tazele
                val msgs = SmsSyncManager.fetchRecentMessages(this@SyncForegroundService, 100)
                webSocketClient?.sendSmsSyncResponse(msgs)
            }
            onPCNotificationReceived = { notif, fromHostKey, hostName ->
                showPCNotification(notif, hostName)
                // Mesh forwarding: Bilgisayarlar arası bildirim iletimi (Windows <-> Mac)
                webSocketClient?.sendPCNotification(notif, excludeHostKey = fromHostKey)
            }
        }

        // Ağdaki tüm cihazları (Windows PC, Mac vb.) dinle ve hepsine bağlan
        discoveryClient = DiscoveryClient(this) { ip, port, name ->
            Log.d(TAG, "Keşif cihaz buldu: $name ($ip:$port)")
            webSocketClient?.connect(ip, port, name)
        }
        discoveryClient?.start()
    }

    private fun updateNotification(text: String) {
        val nm = getSystemService(Context.NOTIFICATION_SERVICE) as? NotificationManager
        nm?.notify(NOTIF_ID, createServiceNotification(text))
    }

    fun showPCNotification(notif: com.sync.android.model.NotificationPayload, sourceHostName: String) {
        val nm = getSystemService(Context.NOTIFICATION_SERVICE) as? NotificationManager ?: return
        val notifId = (notif.id.hashCode() and 0x7FFFFFFF) + 2000

        val app = if (notif.app_name.isNotBlank()) notif.app_name else "Bilgisayar"
        val title = if (notif.title.isNotBlank()) notif.title else app
        val body = if (notif.text.isNotBlank()) notif.text else notif.title

        val clickIntent = Intent(this, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP
        }
        val pendingClick = PendingIntent.getActivity(
            this,
            notifId,
            clickIntent,
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )

        val builder = NotificationCompat.Builder(this, CHANNEL_PC_NOTIF_ID)
            .setSmallIcon(android.R.drawable.stat_notify_chat)
            .setContentTitle("💻 [$app] $title")
            .setContentText(body)
            .setStyle(NotificationCompat.BigTextStyle().bigText(body))
            .setSubText(sourceHostName)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setDefaults(NotificationCompat.DEFAULT_ALL)
            .setContentIntent(pendingClick)
            .setAutoCancel(true)

        nm.notify(notifId, builder.build())
        Log.d(TAG, "Telefonda PC bildirimi gösterildi: [$app] $title - $body")
    }

    private fun initClipboard() {
        clipboardManager = getSystemService(Context.CLIPBOARD_SERVICE) as? ClipboardManager
        clipboardManager?.addPrimaryClipChangedListener {
            val clip = clipboardManager?.primaryClip
            if (clip != null && clip.itemCount > 0) {
                val text = clip.getItemAt(0).text?.toString() ?: ""
                if (text.isNotEmpty() && text != lastLocalClipboard) {
                    lastLocalClipboard = text
                    // Tüm bağlı bilgisayarlara pano metnini gönder
                    webSocketClient?.sendClipboard(text)
                    onClipboardUpdate?.invoke(text)
                    Log.d(TAG, "Pano değişti, tüm bilgisayarlara iletildi: ${text.take(30)}")
                }
            }
        }
    }

    fun initCallStateListener() {
        val tm = getSystemService(Context.TELEPHONY_SERVICE) as? TelephonyManager ?: return

        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                tm.registerTelephonyCallback(
                    mainExecutor,
                    object : TelephonyCallback(), TelephonyCallback.CallStateListener {
                        override fun onCallStateChanged(state: Int) {
                            handleCallState(state)
                        }
                    }
                )
            } else {
                @Suppress("DEPRECATION")
                tm.listen(object : PhoneStateListener() {
                    @Deprecated("Deprecated in Java")
                    override fun onCallStateChanged(state: Int, phoneNumber: String?) {
                        handleCallState(state, phoneNumber ?: "")
                    }
                }, PhoneStateListener.LISTEN_CALL_STATE)
            }
            Log.d(TAG, "Telephony listener başarıyla kaydedildi.")
        } catch (e: SecurityException) {
            Log.w(TAG, "Telephony permission not granted yet: ${e.message}")
        }
    }

    private fun handleCallState(state: Int, number: String = "") {
        val stateStr = when (state) {
            TelephonyManager.CALL_STATE_RINGING -> "RINGING"
            TelephonyManager.CALL_STATE_OFFHOOK -> "OFFHOOK"
            else -> "IDLE"
        }
        Log.d(TAG, "Çağrı durumu değişti: $stateStr")
        // Tüm bağlı bilgisayarlara arama durumunu gönder (hepsinde müzik durur ve bildirim çıkar)
        webSocketClient?.sendCallState(stateStr, number)
    }

    fun connectDirectly(ip: String, port: Int = 42424) {
        webSocketClient?.connect(ip, port, "Doğrudan ($ip)")
    }

    fun restartDiscovery() {
        discoveryClient?.stop()
        webSocketClient?.disconnect()
        discoveryClient?.start()
    }

    private fun initSmsSync() {
        SmsSyncManager.startSmsObserver(this) { newMsg ->
            webSocketClient?.sendNewSmsMessage(newMsg)
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        instance = null
        SmsSyncManager.stopSmsObserver(this)
        mediaReporterHandler.removeCallbacks(mediaReporterRunnable)
        discoveryClient?.stop()
        webSocketClient?.disconnect()
        try {
            if (wakeLock?.isHeld == true) wakeLock?.release()
            if (wifiLock?.isHeld == true) wifiLock?.release()
        } catch (e: Exception) {
            Log.w(TAG, "Lock release error: ${e.message}")
        }
    }

    override fun onBind(intent: Intent?): IBinder? = null
}
