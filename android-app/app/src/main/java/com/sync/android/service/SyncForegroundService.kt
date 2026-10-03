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

class SyncForegroundService : Service() {

    companion object {
        const val CHANNEL_ID = "mac_sync_channel"
        const val NOTIF_ID = 1001
        var instance: SyncForegroundService? = null
    }

    private val TAG = "SyncForegroundService"
    var webSocketClient: SyncWebSocketClient? = null
    private var discoveryClient: DiscoveryClient? = null
    private var clipboardManager: ClipboardManager? = null
    private var lastLocalClipboard: String = ""
    private var wakeLock: android.os.PowerManager.WakeLock? = null
    private var wifiLock: android.net.wifi.WifiManager.WifiLock? = null

    var onStatusChanged: ((Boolean, String) -> Unit)? = null
    var onClipboardUpdate: ((String) -> Unit)? = null
    var onMacMediaInfoUpdate: ((com.sync.android.model.MediaInfoPayload) -> Unit)? = null
    var lastMacMedia: com.sync.android.model.MediaInfoPayload? = null

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
        val notification = createServiceNotification("Mac aranıyor...")
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            startForeground(NOTIF_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_CONNECTED_DEVICE)
        } else {
            startForeground(NOTIF_ID, notification)
        }

        initNetworking()
        initClipboard()
        initCallStateListener()
        mediaReporterHandler.post(mediaReporterRunnable)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        return START_STICKY
    }

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                "Mac Sync Servisi",
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "Android ve Mac arasındaki yerel senkronizasyon servisi"
            }
            val nm = getSystemService(NotificationManager::class.java)
            nm.createNotificationChannel(channel)
        }
    }

    private fun createServiceNotification(status: String): Notification {
        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setContentTitle("Mac Sync")
            .setContentText(status)
            .setSmallIcon(android.R.drawable.stat_sys_data_bluetooth)
            .setOngoing(true)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .build()
    }

    private fun initNetworking() {
        webSocketClient = SyncWebSocketClient(
            context = this,
            onConnectionChanged = { connected ->
                val statusText = if (connected) "Mac'e Bağlandı 🟢" else "Mac Aranıyor 🟡"
                updateNotification(statusText)
                onStatusChanged?.invoke(connected, statusText)
            },
            onClipboardReceived = { text ->
                if (text != lastLocalClipboard) {
                    lastLocalClipboard = text
                    clipboardManager?.setPrimaryClip(ClipData.newPlainText("Mac Sync", text))
                    onClipboardUpdate?.invoke(text)
                    Log.d(TAG, "Pano Mac'ten güncellendi: ${text.take(30)}")
                }
            }
        ).apply {
            onPhoneCommandReceived = { action ->
                PhoneController.handleAction(this@SyncForegroundService, action)
            }
            onMediaInfoReceived = { info ->
                lastMacMedia = info
                onMacMediaInfoUpdate?.invoke(info)
            }
        }

        discoveryClient = DiscoveryClient(this) { ip, port, name ->
            Log.d(TAG, "Discovery found $name at $ip:$port")
            if (webSocketClient?.isConnected != true) {
                webSocketClient?.connect(ip, port)
                updateNotification("Bağlanıyor: $name")
                onStatusChanged?.invoke(false, "Bağlanıyor: $name ($ip)")
            }
        }
        discoveryClient?.start()
    }

    private fun updateNotification(text: String) {
        val nm = getSystemService(Context.NOTIFICATION_SERVICE) as? NotificationManager
        nm?.notify(NOTIF_ID, createServiceNotification(text))
    }

    private fun initClipboard() {
        clipboardManager = getSystemService(Context.CLIPBOARD_SERVICE) as? ClipboardManager
        clipboardManager?.addPrimaryClipChangedListener {
            val clip = clipboardManager?.primaryClip
            if (clip != null && clip.itemCount > 0) {
                val text = clip.getItemAt(0).text?.toString() ?: ""
                if (text.isNotEmpty() && text != lastLocalClipboard) {
                    lastLocalClipboard = text
                    webSocketClient?.sendClipboard(text)
                    onClipboardUpdate?.invoke(text)
                    Log.d(TAG, "Pano değişti, Mac'e gönderildi: ${text.take(30)}")
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
        webSocketClient?.sendCallState(stateStr, number)
    }

    fun connectDirectly(ip: String, port: Int = 42424) {
        webSocketClient?.connect(ip, port)
        updateNotification("Bağlanıyor: $ip")
        onStatusChanged?.invoke(false, "Bağlanıyor: $ip")
    }

    fun restartDiscovery() {
        discoveryClient?.stop()
        webSocketClient?.disconnect()
        discoveryClient?.start()
    }

    override fun onDestroy() {
        super.onDestroy()
        instance = null
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
