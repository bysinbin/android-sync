package com.sync.android

import android.Manifest
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import android.widget.Button
import android.widget.TextView
import android.widget.EditText
import android.widget.SeekBar
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import androidx.core.app.ActivityCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import com.sync.android.service.AudioStreamManager
import com.sync.android.service.PhoneController
import com.sync.android.service.SyncForegroundService

class MainActivity : AppCompatActivity() {

    private lateinit var tvStatusBadge: TextView
    private lateinit var tvServerInfo: TextView
    private lateinit var tvNotifPermissionStatus: TextView
    private lateinit var tvPhonePermissionStatus: TextView
    private lateinit var tvLastClipboard: TextView

    private lateinit var etServerIp: EditText
    private lateinit var btnDirectConnect: Button
    private lateinit var btnReconnect: Button
    private lateinit var btnGrantNotification: Button
    private lateinit var btnGrantPhone: Button
    private lateinit var btnMediaPrev: Button
    private lateinit var btnMediaPlayPause: Button
    private lateinit var btnMediaNext: Button
    private lateinit var btnMediaRewind: Button
    private lateinit var btnMediaForward: Button
    private lateinit var btnVolDown: Button
    private lateinit var btnVolUp: Button
    private lateinit var sbMediaProgress: SeekBar
    private lateinit var tvMediaProgressPercent: TextView
    private lateinit var tvMediaTitle: TextView
    private lateinit var tvMediaTime: TextView
    private lateinit var tvPhoneMediaStatus: TextView
    private var isUserTrackingSeekBar: Boolean = false

    private lateinit var btnStreamMacAudio: Button
    private lateinit var btnBluetoothAudio: Button
    private lateinit var btnSendTestClipboard: Button
    private lateinit var btnSendTestNotification: Button
    private lateinit var btnSimulateCall: Button

    private fun formatMs(ms: Long): String {
        val totalSec = (ms / 1000).toInt()
        val m = totalSec / 60
        val s = totalSec % 60
        return String.format("%02d:%02d", m, s)
    }

    private val mainHandler = android.os.Handler(android.os.Looper.getMainLooper())
    private val statusRefresher = object : Runnable {
        override fun run() {
            val isConnected = SyncForegroundService.instance?.webSocketClient?.isConnected == true
            if (isConnected) {
                updateStatusUI(true, "Mac'e Bağlandı 🟢")
            }
            // Update local phone media display
            val pMedia = PhoneController.getCurrentMedia(this@MainActivity)
            if (pMedia != null && pMedia.title.isNotEmpty()) {
                val full = if (pMedia.artist.isNotEmpty()) "${pMedia.title} - ${pMedia.artist}" else pMedia.title
                tvPhoneMediaStatus.text = "📱 Telefonda: $full"
            } else {
                tvPhoneMediaStatus.text = "📱 Telefonda çalan: Yok"
            }
            // Update Mac media display if available
            SyncForegroundService.instance?.lastMacMedia?.let { applyMacMediaInfo(it) }
            mainHandler.postDelayed(this, 1500)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        initViews()
        setupListeners()
        requestAppPermissions()

        // Arka plan servisini başlat
        val serviceIntent = Intent(this, SyncForegroundService::class.java)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            startForegroundService(serviceIntent)
        } else {
            startService(serviceIntent)
        }

        bindServiceCallbacks()
        mainHandler.post(statusRefresher)
    }

    override fun onDestroy() {
        super.onDestroy()
        mainHandler.removeCallbacks(statusRefresher)
    }

    override fun onResume() {
        super.onResume()
        checkPermissionsUI()
        bindServiceCallbacks()
        SyncForegroundService.instance?.initCallStateListener()
    }

    private fun initViews() {
        tvStatusBadge = findViewById(R.id.tvStatusBadge)
        tvServerInfo = findViewById(R.id.tvServerInfo)
        tvNotifPermissionStatus = findViewById(R.id.tvNotifPermissionStatus)
        tvPhonePermissionStatus = findViewById(R.id.tvPhonePermissionStatus)
        tvLastClipboard = findViewById(R.id.tvLastClipboard)

        etServerIp = findViewById(R.id.etServerIp)
        btnDirectConnect = findViewById(R.id.btnDirectConnect)
        btnReconnect = findViewById(R.id.btnReconnect)
        btnGrantNotification = findViewById(R.id.btnGrantNotification)
        btnGrantPhone = findViewById(R.id.btnGrantPhone)
        btnMediaPrev = findViewById(R.id.btnMediaPrev)
        btnMediaPlayPause = findViewById(R.id.btnMediaPlayPause)
        btnMediaNext = findViewById(R.id.btnMediaNext)
        btnMediaRewind = findViewById(R.id.btnMediaRewind)
        btnMediaForward = findViewById(R.id.btnMediaForward)
        btnVolDown = findViewById(R.id.btnVolDown)
        btnVolUp = findViewById(R.id.btnVolUp)
        sbMediaProgress = findViewById(R.id.sbMediaProgress)
        tvMediaProgressPercent = findViewById(R.id.tvMediaProgressPercent)
        tvMediaTitle = findViewById(R.id.tvMediaTitle)
        tvMediaTime = findViewById(R.id.tvMediaTime)
        tvPhoneMediaStatus = findViewById(R.id.tvPhoneMediaStatus)
        tvMediaTitle.isSelected = true // for marquee effect

        btnStreamMacAudio = findViewById(R.id.btnStreamMacAudio)
        btnBluetoothAudio = findViewById(R.id.btnBluetoothAudio)
        btnSendTestClipboard = findViewById(R.id.btnSendTestClipboard)
        btnSendTestNotification = findViewById(R.id.btnSendTestNotification)
        btnSimulateCall = findViewById(R.id.btnSimulateCall)
    }

    private fun setupListeners() {
        btnDirectConnect.setOnClickListener {
            val ip = etServerIp.text.toString().trim()
            if (ip.isNotEmpty()) {
                SyncForegroundService.instance?.connectDirectly(ip)
                Toast.makeText(this, "$ip adresine bağlanılıyor...", Toast.LENGTH_SHORT).show()
            } else {
                Toast.makeText(this, "Lütfen Mac IP adresini girin!", Toast.LENGTH_SHORT).show()
            }
        }

        btnReconnect.setOnClickListener {
            SyncForegroundService.instance?.restartDiscovery()
            Toast.makeText(this, "Mac aranıyor...", Toast.LENGTH_SHORT).show()
        }

        btnGrantNotification.setOnClickListener {
            openNotificationListenerSettings()
        }

        btnGrantPhone.setOnClickListener {
            requestAppPermissions()
        }

        // Medya Scrubber Slider (İlerleme Çubuğu)
        sbMediaProgress.setOnSeekBarChangeListener(object : SeekBar.OnSeekBarChangeListener {
            override fun onProgressChanged(seekBar: SeekBar?, progress: Int, fromUser: Boolean) {
                if (fromUser) {
                    tvMediaProgressPercent.text = "%$progress"
                }
            }

            override fun onStartTrackingTouch(seekBar: SeekBar?) {
                isUserTrackingSeekBar = true
            }

            override fun onStopTrackingTouch(seekBar: SeekBar?) {
                isUserTrackingSeekBar = false
                val progress = seekBar?.progress ?: return
                tvMediaProgressPercent.text = "%$progress"
                val ws = SyncForegroundService.instance?.webSocketClient
                if (ws?.isConnected == true) {
                    ws.sendMediaSeekPercent(progress.toDouble())
                    Toast.makeText(this@MainActivity, "Mac medyası %$progress konumuna sarıldı", Toast.LENGTH_SHORT).show()
                }
                // Ayrıca telefonda çalan yerel medya varsa onu da sar
                PhoneController.seekToPercent(this@MainActivity, progress.toFloat())
            }
        })

        // Mac Medya Kontrol Butonları
        btnMediaPlayPause.setOnClickListener { sendMedia("PLAY_PAUSE") }
        btnMediaPrev.setOnClickListener { sendMedia("PREVIOUS") }
        btnMediaNext.setOnClickListener { sendMedia("NEXT") }
        btnMediaRewind.setOnClickListener { sendMedia("SEEK_BACKWARD") }
        btnMediaForward.setOnClickListener { sendMedia("SEEK_FORWARD") }
        btnVolUp.setOnClickListener { sendMedia("VOLUME_UP") }
        btnVolDown.setOnClickListener { sendMedia("VOLUME_DOWN") }

        // Ses Aktarımı Butonları
        btnStreamMacAudio.setOnClickListener {
            val ip = etServerIp.text.toString().trim().ifEmpty { "192.168.254.95" }
            AudioStreamManager.toggleMacAudio(this, ip) { isPlaying ->
                runOnUiThread {
                    if (isPlaying) {
                        btnStreamMacAudio.text = "⏹ Mac Sesini Durdur"
                        Toast.makeText(this, "Mac sesi Wi-Fi üzerinden çalınıyor 🎧", Toast.LENGTH_SHORT).show()
                    } else {
                        btnStreamMacAudio.text = "🎧 Mac Sesini Telefonda Dinle (Wi-Fi)"
                        Toast.makeText(this, "Ses aktarımı durduruldu", Toast.LENGTH_SHORT).show()
                    }
                }
            }
        }

        btnBluetoothAudio.setOnClickListener {
            try {
                startActivity(Intent(Settings.ACTION_BLUETOOTH_SETTINGS))
            } catch (e: Exception) {
                Toast.makeText(this, "Bluetooth ayarları açılamadı", Toast.LENGTH_SHORT).show()
            }
        }

        // Test Bildirimi
        btnSendTestNotification.setOnClickListener {
            val ws = SyncForegroundService.instance?.webSocketClient
            if (ws?.isConnected == true) {
                ws.sendNotification(
                    id = "test_${System.currentTimeMillis()}",
                    pkg = "com.whatsapp",
                    appName = "WhatsApp",
                    title = "Ahmet Yılmaz",
                    text = "Toplantı saat 16:30'da başlayacak!"
                )
                Toast.makeText(this, "Test bildirimi Mac'e gönderildi!", Toast.LENGTH_SHORT).show()
            } else {
                Toast.makeText(this, "Mac henüz bağlı değil!", Toast.LENGTH_SHORT).show()
            }
        }

        // Arama Simülasyonu
        btnSimulateCall.setOnClickListener {
            val ws = SyncForegroundService.instance?.webSocketClient
            if (ws?.isConnected == true) {
                ws.sendCallState("RINGING", "+90 (555) 123 45 67", "Mehmet Demir")
                Toast.makeText(this, "Arama uyarısı Mac'e iletildi!", Toast.LENGTH_SHORT).show()
            } else {
                Toast.makeText(this, "Mac henüz bağlı değil!", Toast.LENGTH_SHORT).show()
            }
        }

        // Test Pano Gönderimi
        btnSendTestClipboard.setOnClickListener {
            val cm = getSystemService(Context.CLIPBOARD_SERVICE) as? ClipboardManager
            val clip = cm?.primaryClip
            val text = if (clip != null && clip.itemCount > 0) {
                clip.getItemAt(0).text?.toString() ?: ""
            } else ""

            if (text.isNotEmpty()) {
                val ws = SyncForegroundService.instance?.webSocketClient
                if (ws?.isConnected == true) {
                    ws.sendClipboard(text)
                    tvLastClipboard.text = "Mac'e gönderildi: $text"
                    Toast.makeText(this, "Pano Mac'e gönderildi!", Toast.LENGTH_SHORT).show()
                } else {
                    Toast.makeText(this, "Mac henüz bağlı değil!", Toast.LENGTH_SHORT).show()
                }
            } else {
                Toast.makeText(this, "Telefon panosu boş!", Toast.LENGTH_SHORT).show()
            }
        }
    }

    private fun sendMedia(action: String) {
        val ws = SyncForegroundService.instance?.webSocketClient
        if (ws?.isConnected == true) {
            ws.sendMediaCommand(action)
            Toast.makeText(this, "Mac'e komut iletildi: $action", Toast.LENGTH_SHORT).show()
        } else {
            Toast.makeText(this, "Mac bağlı değil!", Toast.LENGTH_SHORT).show()
        }
    }

    private fun bindServiceCallbacks() {
        val service = SyncForegroundService.instance
        if (service != null) {
            val isConnected = service.webSocketClient?.isConnected == true
            updateStatusUI(isConnected, if (isConnected) "Mac'e Bağlandı 🟢" else "Mac Aranıyor 🟡")

            service.onStatusChanged = { connected, text ->
                runOnUiThread { updateStatusUI(connected, text) }
            }

            service.onClipboardUpdate = { text ->
                runOnUiThread {
                    tvLastClipboard.text = "Son Eşitlenen: $text"
                }
            }

            // Immediately apply stored Mac media
            service.lastMacMedia?.let { info ->
                applyMacMediaInfo(info)
            }

            service.onMacMediaInfoUpdate = { info ->
                runOnUiThread {
                    applyMacMediaInfo(info)
                }
            }
        }
    }

    private fun applyMacMediaInfo(info: com.sync.android.model.MediaInfoPayload) {
        if (info.source == "mac" || info.source.isEmpty()) {
            if (info.title.isNotEmpty()) {
                val fullTitle = if (info.artist.isNotEmpty()) "${info.title} - ${info.artist}" else info.title
                tvMediaTitle.text = "🎵 $fullTitle"
            } else {
                tvMediaTitle.text = "🎵 Mac Medyası Çalmıyor"
            }
            tvMediaTime.text = "${formatMs(info.position_ms)} / ${formatMs(info.duration_ms)}"
            tvMediaProgressPercent.text = "%${info.percent.toInt()}"
            if (!isUserTrackingSeekBar) {
                sbMediaProgress.progress = info.percent.toInt()
            }
        }
    }

    private fun updateStatusUI(connected: Boolean, text: String) {
        if (connected) {
            tvStatusBadge.text = "🟢 Bağlandı"
            tvStatusBadge.setTextColor(Color.parseColor("#3FB950"))
            tvServerInfo.text = text
        } else {
            tvStatusBadge.text = "🟡 Aranıyor"
            tvStatusBadge.setTextColor(Color.parseColor("#D29922"))
            tvServerInfo.text = text
        }
    }

    private fun checkPermissionsUI() {
        // 1. Notification Listener Check
        val notifGranted = isNotificationListenerEnabled()
        if (notifGranted) {
            tvNotifPermissionStatus.text = "🔔 Bildirim Erişimi: Açık ✅"
            btnGrantNotification.isEnabled = false
            btnGrantNotification.text = "Etkin"
        } else {
            tvNotifPermissionStatus.text = "🔔 Bildirim Erişimi: Kapalı ❌"
            btnGrantNotification.isEnabled = true
            btnGrantNotification.text = "İzin Ver"
        }

        // 2. Phone State Permission Check
        val phoneGranted = ContextCompat.checkSelfPermission(
            this,
            Manifest.permission.READ_PHONE_STATE
        ) == PackageManager.PERMISSION_GRANTED

        if (phoneGranted) {
            tvPhonePermissionStatus.text = "📞 Çağrı Erişimi: Açık ✅"
            btnGrantPhone.isEnabled = false
            btnGrantPhone.text = "Etkin"
        } else {
            tvPhonePermissionStatus.text = "📞 Çağrı Erişimi: Kapalı ❌"
            btnGrantPhone.isEnabled = true
            btnGrantPhone.text = "İzin Ver"
        }
    }

    private fun isNotificationListenerEnabled(): Boolean {
        val packages = NotificationManagerCompat.getEnabledListenerPackages(this)
        return packages.contains(packageName)
    }

    private fun openNotificationListenerSettings() {
        try {
            startActivity(Intent(Settings.ACTION_NOTIFICATION_LISTENER_SETTINGS))
        } catch (e: Exception) {
            Toast.makeText(this, "Ayarlar açılamadı", Toast.LENGTH_SHORT).show()
        }
    }

    private fun requestAppPermissions() {
        val permissions = mutableListOf<String>()

        if (ContextCompat.checkSelfPermission(this, Manifest.permission.READ_PHONE_STATE) != PackageManager.PERMISSION_GRANTED) {
            permissions.add(Manifest.permission.READ_PHONE_STATE)
        }

        if (ContextCompat.checkSelfPermission(this, Manifest.permission.READ_CALL_LOG) != PackageManager.PERMISSION_GRANTED) {
            permissions.add(Manifest.permission.READ_CALL_LOG)
        }

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            if (ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
                permissions.add(Manifest.permission.POST_NOTIFICATIONS)
            }
        }

        if (permissions.isNotEmpty()) {
            ActivityCompat.requestPermissions(this, permissions.toTypedArray(), 100)
        }
    }
}
