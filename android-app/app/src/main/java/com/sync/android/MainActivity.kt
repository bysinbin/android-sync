package com.sync.android

import android.Manifest
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.view.LayoutInflater
import android.view.View
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.appcompat.widget.SwitchCompat
import androidx.core.app.ActivityCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import com.sync.android.network.ConnectedHost
import com.sync.android.security.PairedDeviceManager
import com.sync.android.security.PairedHostConfig
import com.sync.android.service.SyncForegroundService

import android.widget.SeekBar
import com.sync.android.model.MediaInfoPayload
import com.sync.android.service.PhoneController
import com.sync.android.service.SmsSyncManager

class MainActivity : AppCompatActivity() {

    private lateinit var tvStatusBadge: TextView
    private lateinit var tvPhoneModel: TextView
    private lateinit var tvConnectedSummary: TextView

    // Tabs
    private lateinit var tabBtnComputers: TextView
    private lateinit var tabBtnMedia: TextView
    private lateinit var tabBtnClipboard: TextView
    private lateinit var tabBtnSettings: TextView

    // Tab Panels
    private lateinit var panelComputers: LinearLayout
    private lateinit var panelMedia: LinearLayout
    private lateinit var panelClipboard: LinearLayout
    private lateinit var panelSettings: LinearLayout

    // Computers Tab Views
    private lateinit var llComputersContainer: LinearLayout
    private lateinit var llEmptyComputers: LinearLayout
    private lateinit var swMeshClipboard: SwitchCompat
    private lateinit var swRingAllDevices: SwitchCompat
    private lateinit var btnRescan: Button
    private lateinit var etServerIp: EditText
    private lateinit var btnDirectConnect: Button

    // Media Tab Views
    private lateinit var tvMediaSourceBadge: TextView
    private lateinit var tvMediaSourceIcon: TextView
    private lateinit var tvMediaTrackTitle: TextView
    private lateinit var tvMediaTrackArtist: TextView
    private lateinit var sbMediaProgress: SeekBar
    private lateinit var tvMediaTimeCur: TextView
    private lateinit var tvMediaTimeTotal: TextView
    private lateinit var btnMediaRewind15: Button
    private lateinit var btnMediaPrev: Button
    private lateinit var btnMediaPlayPause: Button
    private lateinit var btnMediaNext: Button
    private lateinit var btnMediaForward15: Button
    private lateinit var btnMediaVolDown: Button
    private lateinit var btnMediaVolUp: Button
    private lateinit var btnMediaMute: Button
    private lateinit var btnFindPhoneTest: Button
    private lateinit var btnStopRingPhone: Button

    // Phone Local Media Views
    private lateinit var tvPhoneMediaBadge: TextView
    private lateinit var tvPhoneMediaTitle: TextView
    private lateinit var tvPhoneMediaArtist: TextView
    private lateinit var sbPhoneMediaProgress: SeekBar
    private lateinit var tvPhoneMediaTimeCur: TextView
    private lateinit var tvPhoneMediaTimeTotal: TextView
    private lateinit var btnPhoneMediaRewind15: Button
    private lateinit var btnPhoneMediaPrev: Button
    private lateinit var btnPhoneMediaPlayPause: Button
    private lateinit var btnPhoneMediaNext: Button
    private lateinit var btnPhoneMediaForward15: Button
    private lateinit var btnPhoneMediaVolDown: Button
    private lateinit var btnPhoneMediaVolUp: Button

    private var isUserSeeking: Boolean = false
    private var currentMediaPosMs: Long = 0L
    private var currentMediaDurationMs: Long = 0L
    private var isCurrentMediaPlaying: Boolean = false

    private var isUserSeekingPhone: Boolean = false
    private var currentPhoneMediaPosMs: Long = 0L
    private var currentPhoneMediaDurationMs: Long = 0L
    private var isCurrentPhoneMediaPlaying: Boolean = false

    private val mediaTicker = object : Runnable {
        override fun run() {
            // PC media ticker
            if (isCurrentMediaPlaying && currentMediaDurationMs > 0 && !isUserSeeking) {
                currentMediaPosMs = (currentMediaPosMs + 1000).coerceAtMost(currentMediaDurationMs)
                tvMediaTimeCur.text = formatMs(currentMediaPosMs)
                val progressPercent = ((currentMediaPosMs.toDouble() / currentMediaDurationMs.toDouble()) * 100.0).toInt().coerceIn(0, 100)
                sbMediaProgress.progress = progressPercent
            }
            // Phone media ticker
            if (isCurrentPhoneMediaPlaying && currentPhoneMediaDurationMs > 0 && !isUserSeekingPhone) {
                currentPhoneMediaPosMs = (currentPhoneMediaPosMs + 1000).coerceAtMost(currentPhoneMediaDurationMs)
                tvPhoneMediaTimeCur.text = formatMs(currentPhoneMediaPosMs)
                val phoneProgress = ((currentPhoneMediaPosMs.toDouble() / currentPhoneMediaDurationMs.toDouble()) * 100.0).toInt().coerceIn(0, 100)
                sbPhoneMediaProgress.progress = phoneProgress
            }
            mainHandler.postDelayed(this, 1000)
        }
    }

    // Clipboard & SMS Tab Views
    private lateinit var tvLastClipboard: TextView
    private lateinit var etCustomClipInput: EditText
    private lateinit var btnSendTestClipboard: Button
    private lateinit var btnTriggerSmsSync: Button

    // Settings Tab Views
    private lateinit var tvNotifPermissionStatus: TextView
    private lateinit var btnGrantNotification: Button
    private lateinit var tvPhonePermissionStatus: TextView
    private lateinit var btnGrantPhone: Button
    private lateinit var tvSmsPermissionStatus: TextView
    private lateinit var btnGrantSms: Button

    private val mainHandler = Handler(Looper.getMainLooper())
    private var activePairingDialog: AlertDialog? = null

    private val uiRefresher = object : Runnable {
        override fun run() {
            refreshDevicesUI()
            updatePermissionsUI()
            updatePhoneMediaUI()
            mainHandler.postDelayed(this, 2000)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        val root = findViewById<View>(R.id.rootLayout)
        androidx.core.view.ViewCompat.setOnApplyWindowInsetsListener(root) { view, insets ->
            val statusBars = insets.getInsets(androidx.core.view.WindowInsetsCompat.Type.statusBars())
            val navBars = insets.getInsets(androidx.core.view.WindowInsetsCompat.Type.navigationBars())
            view.setPadding(0, statusBars.top, 0, navBars.bottom)
            insets
        }

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
        mainHandler.post(uiRefresher)
        mainHandler.post(mediaTicker)
    }

    override fun onDestroy() {
        super.onDestroy()
        mainHandler.removeCallbacks(uiRefresher)
        mainHandler.removeCallbacks(mediaTicker)
        activePairingDialog?.dismiss()
    }

    override fun onResume() {
        super.onResume()
        bindServiceCallbacks()
        refreshDevicesUI()
        updatePermissionsUI()
        updatePhoneMediaUI()
        SyncForegroundService.instance?.lastMacMedia?.let { updateMediaUI(it) }
    }

    private fun initViews() {
        tvStatusBadge = findViewById(R.id.tvStatusBadge)
        tvPhoneModel = findViewById(R.id.tvPhoneModel)
        tvConnectedSummary = findViewById(R.id.tvConnectedSummary)

        // Tabs
        tabBtnComputers = findViewById(R.id.tabBtnComputers)
        tabBtnMedia = findViewById(R.id.tabBtnMedia)
        tabBtnClipboard = findViewById(R.id.tabBtnClipboard)
        tabBtnSettings = findViewById(R.id.tabBtnSettings)

        // Panels
        panelComputers = findViewById(R.id.panelComputers)
        panelMedia = findViewById(R.id.panelMedia)
        panelClipboard = findViewById(R.id.panelClipboard)
        panelSettings = findViewById(R.id.panelSettings)

        // Computers panel
        llComputersContainer = findViewById(R.id.llComputersContainer)
        llEmptyComputers = findViewById(R.id.llEmptyComputers)
        swMeshClipboard = findViewById(R.id.swMeshClipboard)
        swRingAllDevices = findViewById(R.id.swRingAllDevices)
        btnRescan = findViewById(R.id.btnRescan)
        etServerIp = findViewById(R.id.etServerIp)
        btnDirectConnect = findViewById(R.id.btnDirectConnect)

        // Media panel
        tvMediaSourceBadge = findViewById(R.id.tvMediaSourceBadge)
        tvMediaSourceIcon = findViewById(R.id.tvMediaSourceIcon)
        tvMediaTrackTitle = findViewById(R.id.tvMediaTrackTitle)
        tvMediaTrackArtist = findViewById(R.id.tvMediaTrackArtist)
        sbMediaProgress = findViewById(R.id.sbMediaProgress)
        tvMediaTimeCur = findViewById(R.id.tvMediaTimeCur)
        tvMediaTimeTotal = findViewById(R.id.tvMediaTimeTotal)
        btnMediaRewind15 = findViewById(R.id.btnMediaRewind15)
        btnMediaPrev = findViewById(R.id.btnMediaPrev)
        btnMediaPlayPause = findViewById(R.id.btnMediaPlayPause)
        btnMediaNext = findViewById(R.id.btnMediaNext)
        btnMediaForward15 = findViewById(R.id.btnMediaForward15)
        btnMediaVolDown = findViewById(R.id.btnMediaVolDown)
        btnMediaVolUp = findViewById(R.id.btnMediaVolUp)
        btnMediaMute = findViewById(R.id.btnMediaMute)
        btnFindPhoneTest = findViewById(R.id.btnFindPhoneTest)
        btnStopRingPhone = findViewById(R.id.btnStopRingPhone)

        // Phone Local Media
        tvPhoneMediaBadge = findViewById(R.id.tvPhoneMediaBadge)
        tvPhoneMediaTitle = findViewById(R.id.tvPhoneMediaTitle)
        tvPhoneMediaArtist = findViewById(R.id.tvPhoneMediaArtist)
        sbPhoneMediaProgress = findViewById(R.id.sbPhoneMediaProgress)
        tvPhoneMediaTimeCur = findViewById(R.id.tvPhoneMediaTimeCur)
        tvPhoneMediaTimeTotal = findViewById(R.id.tvPhoneMediaTimeTotal)
        btnPhoneMediaRewind15 = findViewById(R.id.btnPhoneMediaRewind15)
        btnPhoneMediaPrev = findViewById(R.id.btnPhoneMediaPrev)
        btnPhoneMediaPlayPause = findViewById(R.id.btnPhoneMediaPlayPause)
        btnPhoneMediaNext = findViewById(R.id.btnPhoneMediaNext)
        btnPhoneMediaForward15 = findViewById(R.id.btnPhoneMediaForward15)
        btnPhoneMediaVolDown = findViewById(R.id.btnPhoneMediaVolDown)
        btnPhoneMediaVolUp = findViewById(R.id.btnPhoneMediaVolUp)

        // Clipboard & SMS panel
        tvLastClipboard = findViewById(R.id.tvLastClipboard)
        etCustomClipInput = findViewById(R.id.etCustomClipInput)
        btnSendTestClipboard = findViewById(R.id.btnSendTestClipboard)
        btnTriggerSmsSync = findViewById(R.id.btnTriggerSmsSync)

        // Settings panel
        tvNotifPermissionStatus = findViewById(R.id.tvNotifPermissionStatus)
        btnGrantNotification = findViewById(R.id.btnGrantNotification)
        tvPhonePermissionStatus = findViewById(R.id.tvPhonePermissionStatus)
        btnGrantPhone = findViewById(R.id.btnGrantPhone)
        tvSmsPermissionStatus = findViewById(R.id.tvSmsPermissionStatus)
        btnGrantSms = findViewById(R.id.btnGrantSms)

        val deviceManager = PairedDeviceManager.getInstance(this)
        swMeshClipboard.isChecked = deviceManager.meshClipboardEnabled
        swRingAllDevices.isChecked = deviceManager.ringAllDevicesOnCall
        tvPhoneModel.text = "📱 ${Build.MANUFACTURER.capitalize()} ${Build.MODEL} • Windows & Mac"
    }

    private fun setupListeners() {
        val deviceManager = PairedDeviceManager.getInstance(this)

        // Tab Navigation
        tabBtnComputers.setOnClickListener { switchTab(0) }
        tabBtnMedia.setOnClickListener { switchTab(1) }
        tabBtnClipboard.setOnClickListener { switchTab(2) }
        tabBtnSettings.setOnClickListener { switchTab(3) }

        // Settings Switches
        swMeshClipboard.setOnCheckedChangeListener { _, isChecked ->
            deviceManager.meshClipboardEnabled = isChecked
            Toast.makeText(this, if (isChecked) "Çapraz Pano İletimi Açıldı" else "Çapraz Pano İletimi Kapatıldı", Toast.LENGTH_SHORT).show()
        }

        swRingAllDevices.setOnCheckedChangeListener { _, isChecked ->
            deviceManager.ringAllDevicesOnCall = isChecked
            Toast.makeText(this, if (isChecked) "Tüm Bilgisayarlar Çaldırılacak" else "Yalnızca Seçili Cihazlar Çaldırılacak", Toast.LENGTH_SHORT).show()
        }

        // Computers Panel Listeners
        btnRescan.setOnClickListener {
            Toast.makeText(this, "Ağdaki bilgisayarlar taranıyor...", Toast.LENGTH_SHORT).show()
            SyncForegroundService.instance?.discoveryClient?.stop()
            SyncForegroundService.instance?.discoveryClient?.start()
            refreshDevicesUI()
        }

        btnDirectConnect.setOnClickListener {
            val ip = etServerIp.text.toString().trim()
            if (ip.isNotEmpty()) {
                Toast.makeText(this, "$ip adresine bağlanılıyor...", Toast.LENGTH_SHORT).show()
                SyncForegroundService.instance?.webSocketClient?.connect(ip, 42424, "Manuel PC")
                etServerIp.setText("")
                refreshDevicesUI()
            } else {
                Toast.makeText(this, "Lütfen geçerli bir IP adresi girin", Toast.LENGTH_SHORT).show()
            }
        }

        // Permissions Panel Listeners
        btnGrantNotification.setOnClickListener {
            startActivity(Intent(Settings.ACTION_NOTIFICATION_LISTENER_SETTINGS))
        }

        btnGrantPhone.setOnClickListener {
            requestPhonePermissions()
        }

        btnGrantSms.setOnClickListener {
            requestSmsPermissions()
        }

        // Media Panel Controls
        btnMediaRewind15.setOnClickListener {
            SyncForegroundService.instance?.sendMediaCommand("SEEK_BACKWARD")
        }

        btnMediaForward15.setOnClickListener {
            SyncForegroundService.instance?.sendMediaCommand("SEEK_FORWARD")
        }

        btnMediaPlayPause.setOnClickListener {
            SyncForegroundService.instance?.sendMediaCommand("PLAY_PAUSE")
        }

        btnMediaPrev.setOnClickListener {
            SyncForegroundService.instance?.sendMediaCommand("PREV")
        }

        btnMediaNext.setOnClickListener {
            SyncForegroundService.instance?.sendMediaCommand("NEXT")
        }

        btnMediaVolUp.setOnClickListener {
            SyncForegroundService.instance?.sendMediaCommand("VOL_UP")
        }

        btnMediaVolDown.setOnClickListener {
            SyncForegroundService.instance?.sendMediaCommand("VOL_DOWN")
        }

        btnMediaMute.setOnClickListener {
            SyncForegroundService.instance?.sendMediaCommand("MUTE")
        }

        btnFindPhoneTest.setOnClickListener {
            PhoneController.handleAction(this, "RING")
            Toast.makeText(this, "🔔 Telefon çaldırılıyor...", Toast.LENGTH_SHORT).show()
        }

        btnStopRingPhone.setOnClickListener {
            PhoneController.handleAction(this, "STOP_RING")
            Toast.makeText(this, "🔕 Susturuldu", Toast.LENGTH_SHORT).show()
        }

        // Phone Local Media Listeners
        btnPhoneMediaRewind15.setOnClickListener {
            PhoneController.handleAction(this, "SEEK_BACKWARD")
            mainHandler.postDelayed({ updatePhoneMediaUI() }, 300)
        }
        btnPhoneMediaPrev.setOnClickListener {
            PhoneController.handleAction(this, "MEDIA_PREV")
            mainHandler.postDelayed({ updatePhoneMediaUI() }, 300)
        }
        btnPhoneMediaPlayPause.setOnClickListener {
            PhoneController.handleAction(this, "MEDIA_PLAY_PAUSE")
            mainHandler.postDelayed({ updatePhoneMediaUI() }, 300)
        }
        btnPhoneMediaNext.setOnClickListener {
            PhoneController.handleAction(this, "MEDIA_NEXT")
            mainHandler.postDelayed({ updatePhoneMediaUI() }, 300)
        }
        btnPhoneMediaForward15.setOnClickListener {
            PhoneController.handleAction(this, "SEEK_FORWARD")
            mainHandler.postDelayed({ updatePhoneMediaUI() }, 300)
        }
        btnPhoneMediaVolDown.setOnClickListener {
            PhoneController.handleAction(this, "VOLUME_DOWN")
        }
        btnPhoneMediaVolUp.setOnClickListener {
            PhoneController.handleAction(this, "VOLUME_UP")
        }

        sbPhoneMediaProgress.setOnSeekBarChangeListener(object : SeekBar.OnSeekBarChangeListener {
            override fun onProgressChanged(seekBar: SeekBar?, progress: Int, fromUser: Boolean) {
                if (fromUser && currentPhoneMediaDurationMs > 0) {
                    val previewPos = (progress.toDouble() / 100.0) * currentPhoneMediaDurationMs
                    tvPhoneMediaTimeCur.text = formatMs(previewPos.toLong())
                }
            }

            override fun onStartTrackingTouch(seekBar: SeekBar?) {
                isUserSeekingPhone = true
            }

            override fun onStopTrackingTouch(seekBar: SeekBar?) {
                val progress = seekBar?.progress ?: 0
                val percent = progress.toFloat().coerceIn(0f, 100f)
                PhoneController.seekToPercent(this@MainActivity, percent)
                isUserSeekingPhone = false
                mainHandler.postDelayed({ updatePhoneMediaUI() }, 300)
            }
        })

        sbMediaProgress.setOnSeekBarChangeListener(object : SeekBar.OnSeekBarChangeListener {
            override fun onProgressChanged(seekBar: SeekBar?, progress: Int, fromUser: Boolean) {
                if (fromUser && currentMediaDurationMs > 0) {
                    val previewPos = (progress.toDouble() / 100.0) * currentMediaDurationMs
                    tvMediaTimeCur.text = formatMs(previewPos.toLong())
                }
            }

            override fun onStartTrackingTouch(seekBar: SeekBar?) {
                isUserSeeking = true
            }

            override fun onStopTrackingTouch(seekBar: SeekBar?) {
                val progress = seekBar?.progress ?: 0
                val percent = progress.toDouble().coerceIn(0.0, 100.0)
                SyncForegroundService.instance?.sendMediaCommand("SEEK_PERCENT", percent)
                isUserSeeking = false
            }
        })

        // Clipboard & SMS Listeners
        btnSendTestClipboard.setOnClickListener {
            val userText = etCustomClipInput.text.toString().trim()
            val textToSend = if (userText.isNotEmpty()) userText else "AndroidSync Test Metni [${System.currentTimeMillis() % 10000}]"
            val cm = getSystemService(Context.CLIPBOARD_SERVICE) as? ClipboardManager
            cm?.setPrimaryClip(ClipData.newPlainText("AndroidSync", textToSend))
            SyncForegroundService.instance?.webSocketClient?.sendClipboard(textToSend)
            tvLastClipboard.text = textToSend
            etCustomClipInput.setText("")
            Toast.makeText(this, "Pano metni bilgisayarlara iletildi!", Toast.LENGTH_SHORT).show()
        }

        btnTriggerSmsSync.setOnClickListener {
            Toast.makeText(this, "SMS mesajları bilgisayara eşitleniyor...", Toast.LENGTH_SHORT).show()
            val msgs = SmsSyncManager.fetchRecentMessages(this, 100)
            SyncForegroundService.instance?.webSocketClient?.sendSmsSyncResponse(msgs)
            Toast.makeText(this, "✅ ${msgs.size} SMS mesajı senkronize edildi", Toast.LENGTH_SHORT).show()
        }
    }

    private fun switchTab(tabIndex: Int) {
        val tabs = listOf(tabBtnComputers, tabBtnMedia, tabBtnClipboard, tabBtnSettings)
        val panels = listOf(panelComputers, panelMedia, panelClipboard, panelSettings)

        for (i in tabs.indices) {
            if (i == tabIndex) {
                tabs[i].setBackgroundResource(R.drawable.tab_active_bg)
                tabs[i].setTextColor(ContextCompat.getColor(this, R.color.accent_blue))
                panels[i].visibility = View.VISIBLE
            } else {
                tabs[i].setBackgroundResource(R.drawable.tab_inactive_bg)
                tabs[i].setTextColor(ContextCompat.getColor(this, R.color.text_secondary))
                panels[i].visibility = View.GONE
            }
        }
    }

    private fun updateMediaUI(info: MediaInfoPayload) {
        val title = if (info.title.isNotBlank()) info.title else "Medya Çalmıyor"
        val artist = if (info.artist.isNotBlank()) info.artist else "Bilinmeyen Sanatçı"

        tvMediaTrackTitle.text = title
        tvMediaTrackArtist.text = artist
        tvMediaSourceBadge.text = if (info.is_playing) "OYNATILIYOR 🟢" else "DURAKLATILDI ⏸"
        btnMediaPlayPause.text = if (info.is_playing) "⏸ Duraklat" else "▶ Oynat"

        currentMediaDurationMs = info.duration_ms
        currentMediaPosMs = info.position_ms
        isCurrentMediaPlaying = info.is_playing

        tvMediaTimeCur.text = formatMs(currentMediaPosMs)
        tvMediaTimeTotal.text = formatMs(currentMediaDurationMs)

        if (!isUserSeeking) {
            val progressPercent = if (info.duration_ms > 0) {
                ((currentMediaPosMs.toDouble() / currentMediaDurationMs.toDouble()) * 100.0).toInt().coerceIn(0, 100)
            } else {
                info.percent.toInt().coerceIn(0, 100)
            }
            sbMediaProgress.progress = progressPercent
        }
    }

    private fun formatMs(ms: Long): String {
        if (ms <= 0) return "00:00"
        val totalSec = ms / 1000
        val minutes = totalSec / 60
        val seconds = totalSec % 60
        return if (minutes >= 60) {
            val hours = minutes / 60
            val remMin = minutes % 60
            String.format("%02d:%02d:%02d", hours, remMin, seconds)
        } else {
            String.format("%02d:%02d", minutes, seconds)
        }
    }

    private fun updatePhoneMediaUI() {
        val phoneMedia = PhoneController.getCurrentMedia(this)
        if (phoneMedia != null && phoneMedia.title.isNotBlank()) {
            tvPhoneMediaTitle.text = phoneMedia.title
            tvPhoneMediaArtist.text = if (phoneMedia.artist.isNotBlank()) phoneMedia.artist else "Bilinmeyen Sanatçı"
            tvPhoneMediaBadge.text = if (phoneMedia.is_playing) "OYNATILIYOR 🟢" else "DURAKLATILDI ⏸"
            btnPhoneMediaPlayPause.text = if (phoneMedia.is_playing) "⏸ Durdur" else "▶ Oynat"

            currentPhoneMediaDurationMs = phoneMedia.duration_ms
            currentPhoneMediaPosMs = phoneMedia.position_ms
            isCurrentPhoneMediaPlaying = phoneMedia.is_playing

            tvPhoneMediaTimeCur.text = formatMs(currentPhoneMediaPosMs)
            tvPhoneMediaTimeTotal.text = formatMs(currentPhoneMediaDurationMs)

            if (!isUserSeekingPhone) {
                val progressPercent = if (phoneMedia.duration_ms > 0) {
                    ((currentPhoneMediaPosMs.toDouble() / currentPhoneMediaDurationMs.toDouble()) * 100.0).toInt().coerceIn(0, 100)
                } else {
                    phoneMedia.percent.toInt().coerceIn(0, 100)
                }
                sbPhoneMediaProgress.progress = progressPercent
            }
        } else {
            tvPhoneMediaTitle.text = "Telefonda Çalan Medya Yok"
            tvPhoneMediaArtist.text = "Telefonda müzik veya podcast açın"
            tvPhoneMediaBadge.text = "BEKLEMEDE ⚪"
            btnPhoneMediaPlayPause.text = "▶ Oynat"
            isCurrentPhoneMediaPlaying = false
            currentPhoneMediaPosMs = 0L
            currentPhoneMediaDurationMs = 0L
            tvPhoneMediaTimeCur.text = "00:00"
            tvPhoneMediaTimeTotal.text = "00:00"
            sbPhoneMediaProgress.progress = 0
        }
    }

    private fun bindServiceCallbacks() {
        val service = SyncForegroundService.instance ?: return
        service.onClipboardUpdate = { text ->
            runOnUiThread {
                tvLastClipboard.text = text
            }
        }
        service.onMacMediaInfoUpdate = { info ->
            runOnUiThread {
                updateMediaUI(info)
            }
        }
        service.webSocketClient?.onPairingRequested = { host, pin ->
            runOnUiThread {
                showPairingRequestDialog(host, pin)
            }
        }
        service.webSocketClient?.onPairingConfirmed = { host ->
            runOnUiThread {
                Toast.makeText(this, "✅ ${host.name} ile güvenli eşleştirme tamamlandı!", Toast.LENGTH_LONG).show()
                refreshDevicesUI()
            }
        }
    }

    private fun showPairingRequestDialog(host: ConnectedHost, pin: String) {
        if (isFinishing || isDestroyed) return
        if (activePairingDialog?.isShowing == true) return

        activePairingDialog = AlertDialog.Builder(this)
            .setTitle("🔒 Yeni Bilgisayar Bağlantı İsteği")
            .setMessage(
                "${host.name}\nIP: ${host.ip}:${host.port}\n\n" +
                "Bilgisayar ekranındaki 6 haneli kod:\n\n" +
                "👉  $pin  👈\n\n" +
                "Bu bilgisayarın arama, SMS, pano ve medya verilerinize güvenli erişmesine izin veriyor musunuz?"
            )
            .setCancelable(false)
            .setPositiveButton("İzin Ver ve Eşleştir") { dialog, _ ->
                SyncForegroundService.instance?.webSocketClient?.approvePairing(host.key)
                refreshDevicesUI()
                dialog.dismiss()
            }
            .setNegativeButton("Reddet") { dialog, _ ->
                SyncForegroundService.instance?.webSocketClient?.rejectPairing(host.key)
                refreshDevicesUI()
                dialog.dismiss()
            }
            .show()
    }

    private fun refreshDevicesUI() {
        val wsClient = SyncForegroundService.instance?.webSocketClient
        val connectedHosts = wsClient?.connectedHosts ?: emptyList()
        val manager = PairedDeviceManager.getInstance(this)
        val pairedConfigs = manager.getAllDevices()

        val activeCount = connectedHosts.count { it.isConnected }
        val authCount = connectedHosts.count { it.isConnected && it.isAuthorized }

        tvConnectedSummary.text = "$activeCount Bağlı ($authCount Güvenli)"
        if (activeCount == 0) {
            tvStatusBadge.text = "⚪ Cihaz Aranıyor"
            tvStatusBadge.setTextColor(Color.parseColor("#D29922"))
        } else if (authCount > 0) {
            tvStatusBadge.text = "🟢 $authCount Bilgisayar Bağlı"
            tvStatusBadge.setTextColor(Color.parseColor("#3FB950"))
        } else {
            tvStatusBadge.text = "🟡 Eşleşme Bekleniyor"
            tvStatusBadge.setTextColor(Color.parseColor("#D29922"))
        }

        // All devices to display: Merge active connected hosts and previously paired hosts
        val allDeviceKeys = mutableSetOf<String>()
        val displayList = mutableListOf<Pair<ConnectedHost?, PairedHostConfig?>>()

        for (host in connectedHosts) {
            val paired = manager.getDevice(host.clientId)
            displayList.add(Pair(host, paired))
            allDeviceKeys.add(host.clientId)
            allDeviceKeys.add(host.key)
        }

        for (paired in pairedConfigs) {
            if (!allDeviceKeys.contains(paired.clientId)) {
                displayList.add(Pair(null, paired))
            }
        }

        // Clear container except empty placeholder
        llComputersContainer.removeAllViews()

        if (displayList.isEmpty()) {
            llComputersContainer.addView(llEmptyComputers)
            llEmptyComputers.visibility = View.VISIBLE
            return
        }
        llEmptyComputers.visibility = View.GONE

        val inflater = LayoutInflater.from(this)
        for ((host, paired) in displayList) {
            val cardView = inflater.inflate(R.layout.item_computer_card, llComputersContainer, false)

            val tvOsIcon = cardView.findViewById<TextView>(R.id.tvOsIcon)
            val tvComputerName = cardView.findViewById<TextView>(R.id.tvComputerName)
            val tvComputerIp = cardView.findViewById<TextView>(R.id.tvComputerIp)
            val tvBadge = cardView.findViewById<TextView>(R.id.tvDeviceStatusBadge)

            val swClipboard = cardView.findViewById<SwitchCompat>(R.id.swDevClipboard)
            val swCalls = cardView.findViewById<SwitchCompat>(R.id.swDevCalls)
            val swSms = cardView.findViewById<SwitchCompat>(R.id.swDevSms)
            val swMedia = cardView.findViewById<SwitchCompat>(R.id.swDevMedia)

            val btnPairNow = cardView.findViewById<Button>(R.id.btnPairNow)
            val btnUnpair = cardView.findViewById<Button>(R.id.btnUnpair)
            val btnReconnect = cardView.findViewById<Button>(R.id.btnReconnect)

            val os = host?.os ?: paired?.os ?: "windows"
            val name = host?.name ?: paired?.name ?: "Bilinmeyen Bilgisayar"
            val ip = host?.ip ?: paired?.lastIp ?: "0.0.0.0"
            val port = host?.port ?: paired?.lastPort ?: 42424
            val clientId = host?.clientId ?: paired?.clientId ?: host?.key ?: ""

            tvOsIcon.text = if (os.lowercase() == "mac") "🍏" else "🪟"
            tvComputerName.text = name
            tvComputerIp.text = "IP: $ip:$port"

            val isConnected = host?.isConnected == true
            val isAuthorized = host?.isAuthorized == true || (paired != null && paired.isPaired && isConnected)

            if (isConnected && isAuthorized) {
                tvBadge.text = "🟢 Bağlı & Eşleşti"
                tvBadge.setTextColor(Color.parseColor("#3FB950"))
                btnPairNow.visibility = View.GONE
                btnUnpair.visibility = View.VISIBLE
            } else if (isConnected && !isAuthorized) {
                tvBadge.text = "🟡 Eşleşme Bekliyor"
                tvBadge.setTextColor(Color.parseColor("#D29922"))
                btnPairNow.visibility = View.VISIBLE
                btnPairNow.text = "🔑 Eşleştir (${host?.pairingPin ?: ""})"
                btnUnpair.visibility = View.GONE
            } else {
                tvBadge.text = "🔴 Çevrimdışı"
                tvBadge.setTextColor(Color.parseColor("#8B949E"))
                btnPairNow.visibility = View.GONE
                btnUnpair.visibility = if (paired != null) View.VISIBLE else View.GONE
            }

            // Set Switch states
            swClipboard.isChecked = paired?.allowClipboard != false
            swCalls.isChecked = paired?.allowCalls != false
            swSms.isChecked = paired?.allowSms != false
            swMedia.isChecked = paired?.allowMedia != false

            // Switch toggle listeners
            swClipboard.setOnCheckedChangeListener { _, isChecked ->
                manager.updatePermissions(clientId, isChecked, swCalls.isChecked, swSms.isChecked, swMedia.isChecked)
            }
            swCalls.setOnCheckedChangeListener { _, isChecked ->
                manager.updatePermissions(clientId, swClipboard.isChecked, isChecked, swSms.isChecked, swMedia.isChecked)
            }
            swSms.setOnCheckedChangeListener { _, isChecked ->
                manager.updatePermissions(clientId, swClipboard.isChecked, swCalls.isChecked, isChecked, swMedia.isChecked)
            }
            swMedia.setOnCheckedChangeListener { _, isChecked ->
                manager.updatePermissions(clientId, swClipboard.isChecked, swCalls.isChecked, swSms.isChecked, isChecked)
            }

            btnPairNow.setOnClickListener {
                if (host != null) {
                    showPairingRequestDialog(host, host.pairingPin)
                }
            }

            btnUnpair.setOnClickListener {
                AlertDialog.Builder(this)
                    .setTitle("Eşleşmeyi Kaldır")
                    .setMessage("$name bilgisayarı ile olan güvenli eşleşmeyi kaldırmak istiyor musunuz?")
                    .setPositiveButton("Kaldır") { _, _ ->
                        wsClient?.unpairHost(clientId)
                        Toast.makeText(this, "Eşleşme kaldırıldı", Toast.LENGTH_SHORT).show()
                        refreshDevicesUI()
                    }
                    .setNegativeButton("İptal", null)
                    .show()
            }

            btnReconnect.setOnClickListener {
                Toast.makeText(this, "$ip adresine yeniden bağlanılıyor...", Toast.LENGTH_SHORT).show()
                wsClient?.connect(ip, port, name)
                refreshDevicesUI()
            }

            llComputersContainer.addView(cardView)
        }
    }

    private fun updatePermissionsUI() {
        // Notification Listener
        val notifGranted = NotificationManagerCompat.getEnabledListenerPackages(this).contains(packageName)
        if (notifGranted) {
            tvNotifPermissionStatus.text = "İzin Verildi 🟢"
            tvNotifPermissionStatus.setTextColor(Color.parseColor("#3FB950"))
            btnGrantNotification.visibility = View.GONE
        } else {
            tvNotifPermissionStatus.text = "İzin Gerekli 🔴"
            tvNotifPermissionStatus.setTextColor(Color.parseColor("#F85149"))
            btnGrantNotification.visibility = View.VISIBLE
        }

        // Phone & Calls
        val phoneGranted = ContextCompat.checkSelfPermission(this, Manifest.permission.READ_PHONE_STATE) == PackageManager.PERMISSION_GRANTED
        if (phoneGranted) {
            tvPhonePermissionStatus.text = "İzin Verildi 🟢"
            tvPhonePermissionStatus.setTextColor(Color.parseColor("#3FB950"))
            btnGrantPhone.visibility = View.GONE
        } else {
            tvPhonePermissionStatus.text = "İzin Gerekli 🔴"
            tvPhonePermissionStatus.setTextColor(Color.parseColor("#F85149"))
            btnGrantPhone.visibility = View.VISIBLE
        }

        // SMS
        val smsGranted = ContextCompat.checkSelfPermission(this, Manifest.permission.READ_SMS) == PackageManager.PERMISSION_GRANTED &&
                ContextCompat.checkSelfPermission(this, Manifest.permission.SEND_SMS) == PackageManager.PERMISSION_GRANTED
        if (smsGranted) {
            tvSmsPermissionStatus.text = "İzin Verildi 🟢"
            tvSmsPermissionStatus.setTextColor(Color.parseColor("#3FB950"))
            btnGrantSms.visibility = View.GONE
        } else {
            tvSmsPermissionStatus.text = "İzin Gerekli 🔴"
            tvSmsPermissionStatus.setTextColor(Color.parseColor("#F85149"))
            btnGrantSms.visibility = View.VISIBLE
        }
    }

    private fun requestAppPermissions() {
        val permissions = mutableListOf(
            Manifest.permission.READ_PHONE_STATE,
            Manifest.permission.READ_CALL_LOG,
            Manifest.permission.READ_SMS,
            Manifest.permission.SEND_SMS,
            Manifest.permission.RECEIVE_SMS,
            Manifest.permission.READ_CONTACTS
        )
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            permissions.add(Manifest.permission.ANSWER_PHONE_CALLS)
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            permissions.add(Manifest.permission.POST_NOTIFICATIONS)
        }
        ActivityCompat.requestPermissions(this, permissions.toTypedArray(), 101)
    }

    private fun requestPhonePermissions() {
        val permissions = mutableListOf(Manifest.permission.READ_PHONE_STATE)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            permissions.add(Manifest.permission.ANSWER_PHONE_CALLS)
            permissions.add(Manifest.permission.CALL_PHONE)
        }
        ActivityCompat.requestPermissions(this, permissions.toTypedArray(), 102)
    }

    private fun requestSmsPermissions() {
        val permissions = arrayOf(
            Manifest.permission.READ_SMS,
            Manifest.permission.SEND_SMS,
            Manifest.permission.RECEIVE_SMS,
            Manifest.permission.READ_CONTACTS
        )
        ActivityCompat.requestPermissions(this, permissions, 103)
    }
}
