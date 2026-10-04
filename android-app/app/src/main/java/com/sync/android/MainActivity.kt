package com.sync.android

import android.Manifest
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.graphics.Typeface
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.view.Gravity
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
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricPrompt
import com.sync.android.network.ConnectedHost
import com.sync.android.security.PairedDeviceManager
import com.sync.android.security.PairedHostConfig
import com.sync.android.service.SyncForegroundService
import android.hardware.Sensor
import android.hardware.SensorEvent
import android.hardware.SensorEventListener
import android.hardware.SensorManager

import android.widget.SeekBar
import android.net.Uri
import android.annotation.SuppressLint
import android.view.MotionEvent
import android.view.HapticFeedbackConstants
import android.widget.FrameLayout
import com.sync.android.model.MediaInfoPayload
import com.sync.android.model.TouchpadEventPayload
import com.sync.android.service.PhoneController
import com.sync.android.service.SmsSyncManager
import com.sync.android.service.FileManager
import com.sync.android.service.ContactsManager

class MainActivity : AppCompatActivity() {

    companion object {
        var instance: MainActivity? = null
    }

    val screenCaptureLauncher = registerForActivityResult(
        androidx.activity.result.contract.ActivityResultContracts.StartActivityForResult()
    ) { result ->
        if (result.resultCode == android.app.Activity.RESULT_OK && result.data != null) {
            try {
                // Android 14+ Zorunluluğu: getMediaProjection öncesinde servis type mediaProjection olarak güncellenmelidir
                SyncForegroundService.instance?.updateForegroundTypeForScreenMirror(true)

                val projectionManager = getSystemService(Context.MEDIA_PROJECTION_SERVICE) as android.media.projection.MediaProjectionManager
                val mediaProjection = projectionManager.getMediaProjection(result.resultCode, result.data!!)
                if (mediaProjection != null) {
                    SyncForegroundService.instance?.screenMirrorManager?.setMediaProjection(mediaProjection)
                    SyncForegroundService.instance?.screenMirrorManager?.startMirroring()
                    Toast.makeText(this, "✅ Ekran Yansıtma Başlatıldı", Toast.LENGTH_SHORT).show()
                } else {
                    Toast.makeText(this, "❌ Ekran Yansıtma İzni Alınamadı", Toast.LENGTH_SHORT).show()
                }
            } catch (e: Exception) {
                android.util.Log.e("MainActivity", "getMediaProjection başlatma hatası: ${e.message}", e)
                Toast.makeText(this, "Ekran Yansıtma Hatası: ${e.message}", Toast.LENGTH_LONG).show()
            }
        } else {
            Toast.makeText(this, "❌ Ekran Yansıtma İzni Reddedildi", Toast.LENGTH_SHORT).show()
        }
    }

    fun requestScreenCapture() {
        val projectionManager = getSystemService(Context.MEDIA_PROJECTION_SERVICE) as? android.media.projection.MediaProjectionManager
        if (projectionManager != null) {
            screenCaptureLauncher.launch(projectionManager.createScreenCaptureIntent())
        }
    }

    private lateinit var tvStatusBadge: TextView
    private lateinit var tvPhoneModel: TextView
    private lateinit var tvConnectedSummary: TextView

    // Tabs
    private lateinit var tabBtnComputers: TextView
    private lateinit var tabBtnTouchpad: TextView
    private lateinit var tabBtnMedia: TextView
    private lateinit var tabBtnClipboard: TextView
    private lateinit var tabBtnFiles: TextView
    private lateinit var tabBtnSettings: TextView

    // Tab Panels
    private lateinit var panelComputers: LinearLayout
    private lateinit var panelTouchpad: LinearLayout
    private lateinit var panelMedia: LinearLayout
    private lateinit var panelClipboard: LinearLayout
    private lateinit var panelFiles: LinearLayout
    private lateinit var panelSettings: LinearLayout

    // Touchpad & Remote Views
    private lateinit var btnTouchpadSensitivity: Button
    private lateinit var vTouchpadSurface: FrameLayout
    private lateinit var btnMouseLeft: Button
    private lateinit var btnMouseMiddle: Button
    private lateinit var btnMouseRight: Button
    private lateinit var btnRemotePrevSlide: Button
    private lateinit var btnRemoteStartF5: Button
    private lateinit var btnRemoteNextSlide: Button
    private lateinit var btnRemoteEsc: Button
    private lateinit var btnRemoteSpace: Button
    private lateinit var btnRemoteEnter: Button
    private lateinit var btnRemoteLaserPointer: Button
    private lateinit var btnBiometricUnlock: Button
    private lateinit var etUnlockPin: EditText
    private lateinit var btnSaveUnlockPin: Button
    private var touchpadSensitivity: Float = 1.5f

    // Gyroscope Air Mouse & Laser Pointer
    private var sensorManager: SensorManager? = null
    private var gyroSensor: Sensor? = null
    private var isAirMouseActive = false
    private val gyroSensitivity: Float = 16.0f
    private var gyroListener: SensorEventListener? = null

    // File Sharing & URL Views
    private lateinit var btnSelectAndSendFile: Button
    private lateinit var tvFileTransferStatus: TextView
    private lateinit var btnOpenDownloadsFolder: Button
    private lateinit var etWebUrlInput: EditText
    private lateinit var btnSendUrlToPC: Button
    private lateinit var btnPasteAndSendUrl: Button

    // Contacts Views
    private lateinit var btnTriggerContactsSync: Button
    private lateinit var tvContactsPermissionStatus: TextView
    private lateinit var btnGrantContacts: Button
    private lateinit var btnTriggerPhotosSync: Button
    private lateinit var tvPhotosPermissionStatus: TextView
    private lateinit var btnGrantPhotos: Button

    private val filePickerLauncher = registerForActivityResult(
        androidx.activity.result.contract.ActivityResultContracts.GetMultipleContents()
    ) { uris ->
        if (!uris.isNullOrEmpty()) {
            sendFilesToConnectedHosts(uris)
        }
    }

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

    // Multi-Host Target Selector Views
    private lateinit var tvTouchpadSelectedHost: TextView
    private lateinit var llTouchpadTargetPills: LinearLayout
    private lateinit var tvMediaSelectedHost: TextView
    private lateinit var llMediaTargetPills: LinearLayout
    private lateinit var tvClipboardSelectedHost: TextView
    private lateinit var llClipboardTargetPills: LinearLayout
    private lateinit var tvFilesSelectedHost: TextView
    private lateinit var llFilesTargetPills: LinearLayout

    // Multi-Host Target Selection State (null = All/Broadcast)
    private var selectedTouchpadHostKey: String? = null
    private var selectedMediaHostKey: String? = null
    private var selectedClipboardHostKey: String? = null
    private var selectedFilesHostKey: String? = null

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
        instance = this
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

        handleIncomingShareIntent(intent)
    }

    override fun onNewIntent(intent: Intent?) {
        super.onNewIntent(intent)
        setIntent(intent)
        handleIncomingShareIntent(intent)
    }

    private fun handleIncomingShareIntent(intent: Intent?) {
        if (intent == null) return
        val action = intent.action
        val type = intent.type

        if (Intent.ACTION_SEND == action && type != null) {
            val extraText = intent.getStringExtra(Intent.EXTRA_TEXT)
            if (!extraText.isNullOrBlank()) {
                val urlRegex = "(https?://[^\\s]+)".toRegex()
                val match = urlRegex.find(extraText)
                val url = match?.value ?: extraText.trim()
                if (url.startsWith("http://") || url.startsWith("https://")) {
                    switchTab(3)
                    sendUrlToConnectedHosts(url)
                    return
                }
            }

            val streamUri = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                intent.getParcelableExtra(Intent.EXTRA_STREAM, Uri::class.java)
            } else {
                @Suppress("DEPRECATION")
                intent.getParcelableExtra(Intent.EXTRA_STREAM) as? Uri
            }
            if (streamUri != null) {
                switchTab(3)
                sendFilesToConnectedHosts(listOf(streamUri))
            }
        } else if (Intent.ACTION_SEND_MULTIPLE == action && type != null) {
            val streamUris = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                intent.getParcelableArrayListExtra(Intent.EXTRA_STREAM, Uri::class.java)
            } else {
                @Suppress("DEPRECATION")
                intent.getParcelableArrayListExtra<Uri>(Intent.EXTRA_STREAM)
            }
            if (!streamUris.isNullOrEmpty()) {
                switchTab(3)
                sendFilesToConnectedHosts(streamUris)
            }
        }
    }

    private fun sendUrlToConnectedHosts(rawUrl: String) {
        var url = rawUrl.trim()
        if (!url.startsWith("http://") && !url.startsWith("https://")) {
            url = "https://$url"
        }
        val wsClient = SyncForegroundService.instance?.webSocketClient
        val allHosts = wsClient?.connectedHosts?.filter { it.isConnected && it.isAuthorized } ?: emptyList()
        val targetHosts = if (selectedFilesHostKey != null) {
            allHosts.filter { it.key == selectedFilesHostKey }
        } else {
            allHosts
        }
        if (targetHosts.isEmpty()) {
            Toast.makeText(this, "⚠️ Bağlantı göndermek için hedef bilgisayar bağlı değil!", Toast.LENGTH_LONG).show()
            return
        }
        wsClient?.sendOpenUrl(url, targetHostKey = selectedFilesHostKey)
        val names = targetHosts.map { it.name }.joinToString(", ")
        Toast.makeText(this, "🌐 Bağlantı bilgisayarda açılıyor: $names", Toast.LENGTH_SHORT).show()
    }

    private fun sendFilesToConnectedHosts(uris: List<Uri>) {
        val wsClient = SyncForegroundService.instance?.webSocketClient
        val allHosts = wsClient?.connectedHosts?.filter { it.isConnected && it.isAuthorized } ?: emptyList()
        val targetHosts = if (selectedFilesHostKey != null) {
            allHosts.filter { it.key == selectedFilesHostKey }
        } else {
            allHosts
        }
        if (targetHosts.isEmpty()) {
            Toast.makeText(this, "⚠️ Dosya göndermek için hedef bilgisayar bağlı değil!", Toast.LENGTH_LONG).show()
            tvFileTransferStatus.text = "⚠️ Aktarım başarısız: Hedef bilgisayar bağlı değil."
            return
        }

        val targetDesc = if (selectedFilesHostKey != null) targetHosts.first().name else "Tüm Bilgisayarlar (${targetHosts.size})"
        tvFileTransferStatus.text = "⏳ ${uris.size} dosya aktarılıyor -> $targetDesc..."
        for (uri in uris) {
            for (host in targetHosts) {
                val serverUrl = "http://${host.ip}:${host.port}"
                FileManager.uploadFile(this, uri, serverUrl) { success, err ->
                    runOnUiThread {
                        if (success) {
                            tvFileTransferStatus.text = "✅ Dosya başarıyla iletildi (${host.name})"
                        } else {
                            tvFileTransferStatus.text = "❌ Aktarım hatası: ${err ?: "Bilinmeyen hata"}"
                        }
                    }
                }
            }
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        if (instance == this) instance = null
        mainHandler.removeCallbacks(uiRefresher)
        mainHandler.removeCallbacks(mediaTicker)
        activePairingDialog?.dismiss()
        if (isAirMouseActive && gyroListener != null) {
            sensorManager?.unregisterListener(gyroListener)
            isAirMouseActive = false
        }
    }

    override fun onPause() {
        super.onPause()
        if (isAirMouseActive && gyroListener != null) {
            sensorManager?.unregisterListener(gyroListener)
            isAirMouseActive = false
            if (::btnRemoteLaserPointer.isInitialized) {
                btnRemoteLaserPointer.text = "🔴 Hava Faresi & Lazer İşaretçi (Basılı Tutun)"
                btnRemoteLaserPointer.setTextColor(ContextCompat.getColor(this, R.color.accent_red))
            }
        }
    }

    override fun onResume() {
        super.onResume()
        bindServiceCallbacks()
        refreshDevicesUI()
        updatePermissionsUI()
        updatePhoneMediaUI()
        updateTargetHostSelectors()

        val wsClient = SyncForegroundService.instance?.webSocketClient
        val targetHost = wsClient?.connectedHosts?.find { it.key == selectedMediaHostKey }
            ?: wsClient?.connectedHosts?.firstOrNull { it.isConnected && it.isAuthorized }
        if (targetHost != null) {
            selectedMediaHostKey = targetHost.key
            val info = targetHost.lastMedia ?: MediaInfoPayload(title = "Medya Çalmıyor", artist = targetHost.name)
            updateMediaUI(info)
        }
    }

    private fun initViews() {
        tvStatusBadge = findViewById(R.id.tvStatusBadge)
        tvPhoneModel = findViewById(R.id.tvPhoneModel)
        tvConnectedSummary = findViewById(R.id.tvConnectedSummary)

        // Tabs
        tabBtnComputers = findViewById(R.id.tabBtnComputers)
        tabBtnTouchpad = findViewById(R.id.tabBtnTouchpad)
        tabBtnMedia = findViewById(R.id.tabBtnMedia)
        tabBtnClipboard = findViewById(R.id.tabBtnClipboard)
        tabBtnFiles = findViewById(R.id.tabBtnFiles)
        tabBtnSettings = findViewById(R.id.tabBtnSettings)

        // Panels
        panelComputers = findViewById(R.id.panelComputers)
        panelTouchpad = findViewById(R.id.panelTouchpad)
        panelMedia = findViewById(R.id.panelMedia)
        panelClipboard = findViewById(R.id.panelClipboard)
        panelFiles = findViewById(R.id.panelFiles)
        panelSettings = findViewById(R.id.panelSettings)

        // Touchpad views
        btnTouchpadSensitivity = findViewById(R.id.btnTouchpadSensitivity)
        vTouchpadSurface = findViewById(R.id.vTouchpadSurface)
        btnMouseLeft = findViewById(R.id.btnMouseLeft)
        btnMouseMiddle = findViewById(R.id.btnMouseMiddle)
        btnMouseRight = findViewById(R.id.btnMouseRight)
        btnRemotePrevSlide = findViewById(R.id.btnRemotePrevSlide)
        btnRemoteStartF5 = findViewById(R.id.btnRemoteStartF5)
        btnRemoteNextSlide = findViewById(R.id.btnRemoteNextSlide)
        btnRemoteEsc = findViewById(R.id.btnRemoteEsc)
        btnRemoteSpace = findViewById(R.id.btnRemoteSpace)
        btnRemoteEnter = findViewById(R.id.btnRemoteEnter)
        btnRemoteLaserPointer = findViewById(R.id.btnRemoteLaserPointer)
        btnBiometricUnlock = findViewById(R.id.btnBiometricUnlock)
        etUnlockPin = findViewById(R.id.etUnlockPin)
        btnSaveUnlockPin = findViewById(R.id.btnSaveUnlockPin)

        // Files views
        btnSelectAndSendFile = findViewById(R.id.btnSelectAndSendFile)
        tvFileTransferStatus = findViewById(R.id.tvFileTransferStatus)
        btnOpenDownloadsFolder = findViewById(R.id.btnOpenDownloadsFolder)
        etWebUrlInput = findViewById(R.id.etWebUrlInput)
        btnSendUrlToPC = findViewById(R.id.btnSendUrlToPC)
        btnPasteAndSendUrl = findViewById(R.id.btnPasteAndSendUrl)

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
        btnTriggerContactsSync = findViewById(R.id.btnTriggerContactsSync)

        // Settings panel
        tvNotifPermissionStatus = findViewById(R.id.tvNotifPermissionStatus)
        btnGrantNotification = findViewById(R.id.btnGrantNotification)
        tvPhonePermissionStatus = findViewById(R.id.tvPhonePermissionStatus)
        btnGrantPhone = findViewById(R.id.btnGrantPhone)
        tvSmsPermissionStatus = findViewById(R.id.tvSmsPermissionStatus)
        btnGrantSms = findViewById(R.id.btnGrantSms)
        tvContactsPermissionStatus = findViewById(R.id.tvContactsPermissionStatus)
        btnGrantContacts = findViewById(R.id.btnGrantContacts)
        btnTriggerPhotosSync = findViewById(R.id.btnTriggerPhotosSync)
        tvPhotosPermissionStatus = findViewById(R.id.tvPhotosPermissionStatus)
        btnGrantPhotos = findViewById(R.id.btnGrantPhotos)

        // Multi-Host Target Selector views
        tvTouchpadSelectedHost = findViewById(R.id.tvTouchpadSelectedHost)
        llTouchpadTargetPills = findViewById(R.id.llTouchpadTargetPills)
        tvMediaSelectedHost = findViewById(R.id.tvMediaSelectedHost)
        llMediaTargetPills = findViewById(R.id.llMediaTargetPills)
        tvClipboardSelectedHost = findViewById(R.id.tvClipboardSelectedHost)
        llClipboardTargetPills = findViewById(R.id.llClipboardTargetPills)
        tvFilesSelectedHost = findViewById(R.id.tvFilesSelectedHost)
        llFilesTargetPills = findViewById(R.id.llFilesTargetPills)

        val deviceManager = PairedDeviceManager.getInstance(this)
        swMeshClipboard.isChecked = deviceManager.meshClipboardEnabled
        swRingAllDevices.isChecked = deviceManager.ringAllDevicesOnCall
        tvPhoneModel.text = "📱 ${Build.MANUFACTURER.capitalize()} ${Build.MODEL} • Windows & Mac"
    }

    private fun setupListeners() {
        val deviceManager = PairedDeviceManager.getInstance(this)

        // Tab Navigation
        tabBtnComputers.setOnClickListener { switchTab(0) }
        tabBtnTouchpad.setOnClickListener { switchTab(1) }
        tabBtnMedia.setOnClickListener { switchTab(2) }
        tabBtnClipboard.setOnClickListener { switchTab(3) }
        tabBtnFiles.setOnClickListener { switchTab(4) }
        tabBtnSettings.setOnClickListener { switchTab(5) }

        // Setup Touchpad
        setupTouchpadListeners()

        // Files Listeners
        btnSelectAndSendFile.setOnClickListener {
            filePickerLauncher.launch("*/*")
        }

        btnOpenDownloadsFolder.setOnClickListener {
            try {
                val intent = Intent(android.app.DownloadManager.ACTION_VIEW_DOWNLOADS)
                startActivity(intent)
            } catch (e: Exception) {
                Toast.makeText(this, "İndirilenler klasörü açılamadı: ${e.message}", Toast.LENGTH_SHORT).show()
            }
        }

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
            SyncForegroundService.instance?.sendMediaCommand("SEEK_BACKWARD", targetHostKey = selectedMediaHostKey)
        }

        btnMediaForward15.setOnClickListener {
            SyncForegroundService.instance?.sendMediaCommand("SEEK_FORWARD", targetHostKey = selectedMediaHostKey)
        }

        btnMediaPlayPause.setOnClickListener {
            SyncForegroundService.instance?.sendMediaCommand("PLAY_PAUSE", targetHostKey = selectedMediaHostKey)
        }

        btnMediaPrev.setOnClickListener {
            SyncForegroundService.instance?.sendMediaCommand("PREV", targetHostKey = selectedMediaHostKey)
        }

        btnMediaNext.setOnClickListener {
            SyncForegroundService.instance?.sendMediaCommand("NEXT", targetHostKey = selectedMediaHostKey)
        }

        btnMediaVolUp.setOnClickListener {
            SyncForegroundService.instance?.sendMediaCommand("VOL_UP", targetHostKey = selectedMediaHostKey)
        }

        btnMediaVolDown.setOnClickListener {
            SyncForegroundService.instance?.sendMediaCommand("VOL_DOWN", targetHostKey = selectedMediaHostKey)
        }

        btnMediaMute.setOnClickListener {
            SyncForegroundService.instance?.sendMediaCommand("MUTE", targetHostKey = selectedMediaHostKey)
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
                SyncForegroundService.instance?.sendMediaCommand("SEEK_PERCENT", percent, targetHostKey = selectedMediaHostKey)
                isUserSeeking = false
            }
        })

        // Clipboard & SMS Listeners
        btnSendTestClipboard.setOnClickListener {
            val userText = etCustomClipInput.text.toString().trim()
            val textToSend = if (userText.isNotEmpty()) userText else "AndroidSync Test Metni [${System.currentTimeMillis() % 10000}]"
            val cm = getSystemService(Context.CLIPBOARD_SERVICE) as? ClipboardManager
            cm?.setPrimaryClip(ClipData.newPlainText("AndroidSync", textToSend))
            SyncForegroundService.instance?.webSocketClient?.sendClipboard(textToSend, targetHostKey = selectedClipboardHostKey)
            tvLastClipboard.text = textToSend
            etCustomClipInput.setText("")
            val targetName = getHostDisplayName(selectedClipboardHostKey)
            Toast.makeText(this, "Pano metni $targetName iletildi!", Toast.LENGTH_SHORT).show()
        }

        btnTriggerSmsSync.setOnClickListener {
            val targetName = getHostDisplayName(selectedClipboardHostKey)
            Toast.makeText(this, "SMS mesajları $targetName ile eşitleniyor...", Toast.LENGTH_SHORT).show()
            val msgs = SmsSyncManager.fetchRecentMessages(this, 100)
            SyncForegroundService.instance?.webSocketClient?.sendSmsSyncResponse(msgs, targetHostKey = selectedClipboardHostKey)
            Toast.makeText(this, "✅ ${msgs.size} SMS mesajı senkronize edildi ($targetName)", Toast.LENGTH_SHORT).show()
        }

        btnTriggerContactsSync.setOnClickListener {
            val targetName = getHostDisplayName(selectedClipboardHostKey)
            Toast.makeText(this, "Rehber $targetName ile eşitleniyor...", Toast.LENGTH_SHORT).show()
            val contacts = ContactsManager.fetchContacts(this)
            SyncForegroundService.instance?.webSocketClient?.sendContactsResponse(contacts, targetHostKey = selectedClipboardHostKey)
            Toast.makeText(this, "✅ ${contacts.size} kişi senkronize edildi ($targetName)", Toast.LENGTH_SHORT).show()
        }

        btnSendUrlToPC.setOnClickListener {
            val url = etWebUrlInput.text.toString().trim()
            if (url.isEmpty()) {
                Toast.makeText(this, "Lütfen bir web adresi girin!", Toast.LENGTH_SHORT).show()
                return@setOnClickListener
            }
            sendUrlToConnectedHosts(url)
            etWebUrlInput.setText("")
        }

        btnPasteAndSendUrl.setOnClickListener {
            val cm = getSystemService(Context.CLIPBOARD_SERVICE) as? ClipboardManager
            val clip = cm?.primaryClip
            if (clip != null && clip.itemCount > 0) {
                val clipText = clip.getItemAt(0).text?.toString()?.trim() ?: ""
                if (clipText.isNotEmpty()) {
                    etWebUrlInput.setText(clipText)
                    sendUrlToConnectedHosts(clipText)
                    return@setOnClickListener
                }
            }
            Toast.makeText(this, "Panoda kopyalanmış bir bağlantı bulunamadı!", Toast.LENGTH_SHORT).show()
        }

        btnGrantNotification.setOnClickListener {
            startActivity(Intent("android.settings.ACTION_NOTIFICATION_LISTENER_SETTINGS"))
        }

        btnGrantPhone.setOnClickListener {
            requestPhonePermissions()
        }

        btnGrantSms.setOnClickListener {
            requestSmsPermissions()
        }

        btnGrantContacts.setOnClickListener {
            ActivityCompat.requestPermissions(this, arrayOf(Manifest.permission.READ_CONTACTS), 104)
        }

        btnTriggerPhotosSync.setOnClickListener {
            val targetName = getHostDisplayName(selectedFilesHostKey)
            Toast.makeText(this, "Fotoğraflar taranıyor ve $targetName ile eşitleniyor...", Toast.LENGTH_SHORT).show()
            Thread {
                val photos = SyncForegroundService.instance?.photosManager?.fetchRecentPhotos(40) ?: emptyList()
                SyncForegroundService.instance?.webSocketClient?.sendPhotosResponse(photos, targetHostKey = selectedFilesHostKey)
                runOnUiThread {
                    Toast.makeText(this, "✅ ${photos.size} fotoğraf $targetName aktarıldı", Toast.LENGTH_SHORT).show()
                }
            }.start()
        }

        btnGrantPhotos.setOnClickListener {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                ActivityCompat.requestPermissions(this, arrayOf(Manifest.permission.READ_MEDIA_IMAGES), 105)
            } else {
                ActivityCompat.requestPermissions(this, arrayOf(Manifest.permission.READ_EXTERNAL_STORAGE), 105)
            }
        }
    }

    private fun switchTab(tabIndex: Int) {
        val tabs = listOf(tabBtnComputers, tabBtnTouchpad, tabBtnMedia, tabBtnClipboard, tabBtnFiles, tabBtnSettings)
        val panels = listOf(panelComputers, panelTouchpad, panelMedia, panelClipboard, panelFiles, panelSettings)

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

    @SuppressLint("ClickableViewAccessibility")
    private fun setupTouchpadListeners() {
        // Sensitivity Button toggle
        btnTouchpadSensitivity.setOnClickListener {
            touchpadSensitivity = when (touchpadSensitivity) {
                1.0f -> 1.5f
                1.5f -> 2.0f
                2.0f -> 2.5f
                else -> 1.0f
            }
            btnTouchpadSensitivity.text = "⚡ ${touchpadSensitivity}x Hız"
        }

        // Dedicated Mouse Buttons
        btnMouseLeft.setOnClickListener {
            it.performHapticFeedback(HapticFeedbackConstants.KEYBOARD_TAP)
            sendTouchpadClick("left")
        }
        btnMouseRight.setOnClickListener {
            it.performHapticFeedback(HapticFeedbackConstants.KEYBOARD_TAP)
            sendTouchpadClick("right")
        }
        btnMouseMiddle.setOnClickListener {
            it.performHapticFeedback(HapticFeedbackConstants.KEYBOARD_TAP)
            sendTouchpadClick("middle")
        }

        // Remote / Presentation Buttons
        btnRemotePrevSlide.setOnClickListener {
            it.performHapticFeedback(HapticFeedbackConstants.KEYBOARD_TAP)
            sendTouchpadKey("LEFT")
        }
        btnRemoteNextSlide.setOnClickListener {
            it.performHapticFeedback(HapticFeedbackConstants.KEYBOARD_TAP)
            sendTouchpadKey("RIGHT")
        }
        btnRemoteStartF5.setOnClickListener {
            it.performHapticFeedback(HapticFeedbackConstants.KEYBOARD_TAP)
            sendTouchpadKey("F5")
        }
        btnRemoteEsc.setOnClickListener {
            it.performHapticFeedback(HapticFeedbackConstants.KEYBOARD_TAP)
            sendTouchpadKey("ESC")
        }
        btnRemoteSpace.setOnClickListener {
            it.performHapticFeedback(HapticFeedbackConstants.KEYBOARD_TAP)
            sendTouchpadKey("SPACE")
        }
        btnRemoteEnter.setOnClickListener {
            it.performHapticFeedback(HapticFeedbackConstants.KEYBOARD_TAP)
            sendTouchpadKey("ENTER")
        }

        // --- Hardware Gyroscope Air Mouse & Laser Pointer ---
        sensorManager = getSystemService(Context.SENSOR_SERVICE) as? SensorManager
        gyroSensor = sensorManager?.getDefaultSensor(Sensor.TYPE_GYROSCOPE)

        gyroListener = object : SensorEventListener {
            override fun onSensorChanged(event: SensorEvent?) {
                if (!isAirMouseActive || event == null) return
                // event.values[0] = pitch (up/down rotation) -> dy
                // event.values[2] = yaw (left/right turn) -> dx
                val pitch = event.values[0]
                val yaw = event.values[2]

                // Low-pass / deadband threshold to prevent jitter when holding still
                if (Math.abs(pitch) > 0.035f || Math.abs(yaw) > 0.035f) {
                    val dx = -yaw * gyroSensitivity * touchpadSensitivity
                    val dy = -pitch * gyroSensitivity * touchpadSensitivity
                    sendTouchpadMove(dx, dy)
                }
            }
            override fun onAccuracyChanged(sensor: Sensor?, accuracy: Int) {}
        }

        btnRemoteLaserPointer.setOnTouchListener { v, event ->
            when (event.action) {
                MotionEvent.ACTION_DOWN -> {
                    v.performHapticFeedback(HapticFeedbackConstants.LONG_PRESS)
                    if (gyroSensor != null) {
                        isAirMouseActive = true
                        sensorManager?.registerListener(gyroListener, gyroSensor, SensorManager.SENSOR_DELAY_GAME)
                        btnRemoteLaserPointer.text = "🎯 Lazer / Hava Faresi Aktif (Havada Hareket Ettirin)"
                        btnRemoteLaserPointer.setTextColor(ContextCompat.getColor(this, R.color.accent_green))
                    } else {
                        Toast.makeText(this, "Bu cihazda jiroskop donanımı bulunamadı!", Toast.LENGTH_SHORT).show()
                    }
                    true
                }
                MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> {
                    if (isAirMouseActive) {
                        isAirMouseActive = false
                        sensorManager?.unregisterListener(gyroListener)
                        btnRemoteLaserPointer.text = "🔴 Hava Faresi & Lazer İşaretçi (Basılı Tutun)"
                        btnRemoteLaserPointer.setTextColor(ContextCompat.getColor(this, R.color.accent_red))
                        v.performHapticFeedback(HapticFeedbackConstants.KEYBOARD_TAP)
                    }
                    true
                }
                else -> false
            }
        }

        // Biometric Unlock & PIN Setup (Per-Host Configured)
        refreshUnlockPinUI()

        btnSaveUnlockPin.setOnClickListener {
            val pin = etUnlockPin.text.toString().trim()
            val prefs = getSharedPreferences("sync_prefs", Context.MODE_PRIVATE)
            val key = getUnlockPinPrefKey(selectedTouchpadHostKey)
            prefs.edit().putString(key, pin).putString("pc_unlock_pin", pin).apply()
            it.performHapticFeedback(HapticFeedbackConstants.CONFIRM)
            val targetName = getHostDisplayName(selectedTouchpadHostKey)
            Toast.makeText(this, if (pin.isNotEmpty()) "PIN kaydedildi ($targetName) ✅" else "PIN temizlendi ($targetName)", Toast.LENGTH_SHORT).show()
            refreshUnlockPinUI()
        }

        btnBiometricUnlock.setOnClickListener {
            it.performHapticFeedback(HapticFeedbackConstants.KEYBOARD_TAP)
            triggerBiometricUnlock()
        }


        // Gesture Trackpad Surface
        var lastTouchX = 0f
        var lastTouchY = 0f
        var touchDownX = 0f
        var touchDownY = 0f
        var touchDownTime = 0L
        var maxPointers = 1
        var isScrolling = false

        vTouchpadSurface.setOnTouchListener { view, event ->
            when (event.actionMasked) {
                MotionEvent.ACTION_DOWN -> {
                    touchDownTime = System.currentTimeMillis()
                    touchDownX = event.x
                    touchDownY = event.y
                    lastTouchX = event.x
                    lastTouchY = event.y
                    maxPointers = 1
                    isScrolling = false
                    view.parent?.requestDisallowInterceptTouchEvent(true)
                    true
                }
                MotionEvent.ACTION_POINTER_DOWN -> {
                    maxPointers = maxOf(maxPointers, event.pointerCount)
                    if (event.pointerCount >= 2) {
                        isScrolling = true
                        lastTouchY = (event.getY(0) + event.getY(1)) / 2f
                    }
                    true
                }
                MotionEvent.ACTION_MOVE -> {
                    if (event.pointerCount == 1 && !isScrolling) {
                        val rawDx = (event.x - lastTouchX)
                        val rawDy = (event.y - lastTouchY)
                        lastTouchX = event.x
                        lastTouchY = event.y

                        // Precision acceleration curve (Mac/Precision Touchpad style)
                        val distance = Math.hypot(rawDx.toDouble(), rawDy.toDouble()).toFloat()
                        val speedMultiplier = if (distance > 20f) 2.4f else if (distance > 6f) 1.6f else 1.1f
                        val dx = rawDx * touchpadSensitivity * speedMultiplier
                        val dy = rawDy * touchpadSensitivity * speedMultiplier

                        if (Math.abs(dx) > 0.05f || Math.abs(dy) > 0.05f) {
                            sendTouchpadMove(dx, dy)
                        }
                    } else if (event.pointerCount >= 2) {
                        val currentY = (event.getY(0) + event.getY(1)) / 2f
                        val deltaY = currentY - lastTouchY
                        lastTouchY = currentY
                        if (Math.abs(deltaY) > 1.5f) {
                            val scrollAmount = (deltaY * 12f).toInt()
                            sendTouchpadScroll(scrollAmount)
                        }
                    }
                    true
                }

                MotionEvent.ACTION_POINTER_UP -> {
                    val remainingPointerIndex = if (event.actionIndex == 0) 1 else 0
                    if (event.pointerCount <= 2) {
                        isScrolling = false
                        try {
                            lastTouchX = event.getX(remainingPointerIndex)
                            lastTouchY = event.getY(remainingPointerIndex)
                        } catch (e: Exception) {}
                    }
                    true
                }

                MotionEvent.ACTION_UP -> {
                    isScrolling = false
                    view.parent?.requestDisallowInterceptTouchEvent(false)
                    val duration = System.currentTimeMillis() - touchDownTime
                    val dist = Math.hypot((event.x - touchDownX).toDouble(), (event.y - touchDownY).toDouble())
                    if (duration < 280 && dist < 25) {
                        // Tap detected
                        view.performHapticFeedback(HapticFeedbackConstants.KEYBOARD_TAP)
                        if (maxPointers == 1) {
                            sendTouchpadClick("left")
                        } else {
                            sendTouchpadClick("right")
                        }
                    }
                    true
                }
                MotionEvent.ACTION_CANCEL -> {
                    isScrolling = false
                    view.parent?.requestDisallowInterceptTouchEvent(false)
                    true
                }

                else -> false
            }
        }
    }

    private fun sendTouchpadMove(dx: Float, dy: Float) {
        val payload = TouchpadEventPayload(type = "move", dx = dx, dy = dy)
        SyncForegroundService.instance?.sendTouchpadEvent(payload, targetHostKey = selectedTouchpadHostKey)
    }

    private fun sendTouchpadClick(button: String) {
        val payload = TouchpadEventPayload(type = "click", button = button)
        SyncForegroundService.instance?.sendTouchpadEvent(payload, targetHostKey = selectedTouchpadHostKey)
    }

    private fun sendTouchpadScroll(scrollY: Int) {
        val payload = TouchpadEventPayload(type = "scroll", scroll_y = scrollY)
        SyncForegroundService.instance?.sendTouchpadEvent(payload, targetHostKey = selectedTouchpadHostKey)
    }

    private fun sendTouchpadKey(key: String) {
        val payload = TouchpadEventPayload(type = "key", key = key)
        SyncForegroundService.instance?.sendTouchpadEvent(payload, targetHostKey = selectedTouchpadHostKey)
    }

    private fun triggerBiometricUnlock() {
        val biometricManager = BiometricManager.from(this)
        val canAuthenticate = biometricManager.canAuthenticate(
            BiometricManager.Authenticators.BIOMETRIC_STRONG or BiometricManager.Authenticators.DEVICE_CREDENTIAL
        )

        val targetName = getHostDisplayName(selectedTouchpadHostKey)
        val prefs = getSharedPreferences("sync_prefs", Context.MODE_PRIVATE)
        val key = getUnlockPinPrefKey(selectedTouchpadHostKey)
        val pin = prefs.getString(key, null) ?: prefs.getString("pc_unlock_pin", null)

        if (canAuthenticate != BiometricManager.BIOMETRIC_SUCCESS) {
            // Cihazda biyometri veya kilit yoksa, doğrudan kayıtlı PIN ile açmayı dene
            SyncForegroundService.instance?.sendBiometricUnlock(pin, targetHostKey = selectedTouchpadHostKey)
            Toast.makeText(this, "🔐 Kilit açma komutu gönderildi ($targetName)", Toast.LENGTH_SHORT).show()
            return
        }

        val executor = ContextCompat.getMainExecutor(this)
        val biometricPrompt = BiometricPrompt(this, executor, object : BiometricPrompt.AuthenticationCallback() {
            override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) {
                super.onAuthenticationSucceeded(result)
                SyncForegroundService.instance?.sendBiometricUnlock(pin, targetHostKey = selectedTouchpadHostKey)
                Toast.makeText(this@MainActivity, "✅ Parmak izi doğrulandı, $targetName kilidi açılıyor...", Toast.LENGTH_SHORT).show()
            }

            override fun onAuthenticationError(errorCode: Int, errString: CharSequence) {
                super.onAuthenticationError(errorCode, errString)
                if (errorCode != BiometricPrompt.ERROR_USER_CANCELED && errorCode != BiometricPrompt.ERROR_NEGATIVE_BUTTON) {
                    Toast.makeText(this@MainActivity, "Biyometrik Hata: $errString", Toast.LENGTH_SHORT).show()
                }
            }

            override fun onAuthenticationFailed() {
                super.onAuthenticationFailed()
                Toast.makeText(this@MainActivity, "❌ Parmak izi tanınmadı", Toast.LENGTH_SHORT).show()
            }
        })

        val promptInfo = BiometricPrompt.PromptInfo.Builder()
            .setTitle("Bilgisayar Kilidini Aç")
            .setSubtitle("Windows / macOS oturumunu açmak için kimliğinizi doğrulayın")
            .setAllowedAuthenticators(BiometricManager.Authenticators.BIOMETRIC_STRONG or BiometricManager.Authenticators.DEVICE_CREDENTIAL)
            .build()

        biometricPrompt.authenticate(promptInfo)
    }


    private fun updateMediaUI(info: MediaInfoPayload) {
        val title = if (info.title.isNotBlank()) info.title else "Medya Çalmıyor"
        val artist = if (info.artist.isNotBlank()) info.artist else "Bilinmeyen Sanatçı"

        tvMediaTrackTitle.text = title
        tvMediaTrackArtist.text = artist
        tvMediaSourceBadge.text = if (info.is_playing) "OYNATILIYOR 🟢" else if (info.title.isNotBlank()) "DURAKLATILDI ⏸" else "BEKLEMEDE ⚪"
        btnMediaPlayPause.text = if (info.is_playing) "⏸ Duraklat" else "▶ Oynat"

        val wsClient = SyncForegroundService.instance?.webSocketClient
        val curHost = wsClient?.connectedHosts?.find { it.key == selectedMediaHostKey }
        tvMediaSourceIcon.text = if (curHost?.os?.lowercase() == "mac") "🍏" else "🪟"

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

    private fun handleHostMediaUpdate(info: MediaInfoPayload, hostName: String, hostKey: String) {
        val wsClient = SyncForegroundService.instance?.webSocketClient
        val host = wsClient?.connectedHosts?.find { it.key == hostKey }
        if (host != null) {
            host.lastMedia = info
        }

        // Auto-select if nothing selected yet
        if (selectedMediaHostKey == null) {
            selectedMediaHostKey = hostKey
        }

        // ONLY update the active player UI if the update is from the selected host!
        if (selectedMediaHostKey == hostKey) {
            updateMediaUI(info)
        }

        // Refresh pills in place (shows song name and play status for each PC)
        refreshMediaPills()
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
        service.onHostMediaInfoUpdate = { info, hostName, hostKey ->
            runOnUiThread {
                handleHostMediaUpdate(info, hostName, hostKey)
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
                updateTargetHostSelectors()
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
                updateTargetHostSelectors()
                dialog.dismiss()
            }
            .setNegativeButton("Reddet") { dialog, _ ->
                SyncForegroundService.instance?.webSocketClient?.rejectPairing(host.key)
                refreshDevicesUI()
                updateTargetHostSelectors()
                dialog.dismiss()
            }
            .show()
    }

    private fun dpToPx(dp: Int): Int {
        return (dp * resources.displayMetrics.density).toInt()
    }

    private fun getHostDisplayName(hostKey: String?): String {
        if (hostKey == null) return "Tüm Bilgisayarlar"
        val wsClient = SyncForegroundService.instance?.webSocketClient
        val host = wsClient?.connectedHosts?.find { it.key == hostKey }
        return host?.name ?: hostKey
    }

    private fun getUnlockPinPrefKey(hostKey: String?): String {
        return if (hostKey.isNullOrEmpty()) "pc_unlock_pin" else "pc_unlock_pin_$hostKey"
    }

    private fun refreshUnlockPinUI() {
        val prefs = getSharedPreferences("sync_prefs", Context.MODE_PRIVATE)
        val key = getUnlockPinPrefKey(selectedTouchpadHostKey)
        val pin = prefs.getString(key, "") ?: prefs.getString("pc_unlock_pin", "") ?: ""
        etUnlockPin.setText(pin)
        val hostName = getHostDisplayName(selectedTouchpadHostKey)
        btnSaveUnlockPin.text = if (selectedTouchpadHostKey != null) "Kaydet ($hostName)" else "PIN Kaydet"
    }

    private fun createPill(title: String, isSelected: Boolean, onClick: () -> Unit): TextView {
        return TextView(this).apply {
            layoutParams = LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.WRAP_CONTENT,
                dpToPx(34)
            ).apply {
                marginEnd = dpToPx(8)
            }
            gravity = Gravity.CENTER
            setPadding(dpToPx(14), 0, dpToPx(14), 0)
            text = title
            textSize = 12f
            typeface = Typeface.DEFAULT_BOLD
            if (isSelected) {
                setBackgroundResource(R.drawable.tab_active_bg)
                setTextColor(ContextCompat.getColor(context, R.color.accent_blue))
            } else {
                setBackgroundResource(R.drawable.tab_inactive_bg)
                setTextColor(ContextCompat.getColor(context, R.color.text_secondary))
            }
            setOnClickListener {
                performHapticFeedback(HapticFeedbackConstants.KEYBOARD_TAP)
                onClick()
            }
        }
    }

    private fun updateTargetHostSelectors() {
        val wsClient = SyncForegroundService.instance?.webSocketClient
        val connectedHosts = wsClient?.connectedHosts?.filter { it.isConnected && it.isAuthorized } ?: emptyList()

        // Validate selection states (if selected host was disconnected, reset to null)
        if (selectedTouchpadHostKey != null && connectedHosts.none { it.key == selectedTouchpadHostKey }) {
            selectedTouchpadHostKey = null
        }
        if (selectedClipboardHostKey != null && connectedHosts.none { it.key == selectedClipboardHostKey }) {
            selectedClipboardHostKey = null
        }
        if (selectedFilesHostKey != null && connectedHosts.none { it.key == selectedFilesHostKey }) {
            selectedFilesHostKey = null
        }
        if (selectedMediaHostKey != null && connectedHosts.none { it.key == selectedMediaHostKey }) {
            selectedMediaHostKey = connectedHosts.firstOrNull()?.key
        }
        if (selectedMediaHostKey == null && connectedHosts.isNotEmpty()) {
            selectedMediaHostKey = connectedHosts.first().key
        }

        // --- 1. Touchpad Target Selector ---
        tvTouchpadSelectedHost.text = if (selectedTouchpadHostKey == null) {
            "🌐 Tüm Bilgisayarlar (${connectedHosts.size})"
        } else {
            val host = connectedHosts.find { it.key == selectedTouchpadHostKey }
            val icon = if (host?.os?.lowercase() == "mac") "🍏" else "🪟"
            "🎯 $icon ${host?.name ?: selectedTouchpadHostKey}"
        }
        llTouchpadTargetPills.removeAllViews()
        llTouchpadTargetPills.addView(createPill("🌐 Tümü", selectedTouchpadHostKey == null) {
            selectedTouchpadHostKey = null
            updateTargetHostSelectors()
        })
        for (host in connectedHosts) {
            val icon = if (host.os.lowercase() == "mac") "🍏" else "🪟"
            val isSel = selectedTouchpadHostKey == host.key
            llTouchpadTargetPills.addView(createPill("$icon ${host.name}", isSel) {
                selectedTouchpadHostKey = host.key
                updateTargetHostSelectors()
            })
        }
        refreshUnlockPinUI()

        // --- 2. Media Target Selector ---
        refreshMediaPills()

        // --- 3. Clipboard & SMS Target Selector ---
        tvClipboardSelectedHost.text = if (selectedClipboardHostKey == null) {
            "🌐 Tüm Bilgisayarlar (${connectedHosts.size})"
        } else {
            val host = connectedHosts.find { it.key == selectedClipboardHostKey }
            val icon = if (host?.os?.lowercase() == "mac") "🍏" else "🪟"
            "🎯 $icon ${host?.name ?: selectedClipboardHostKey}"
        }
        llClipboardTargetPills.removeAllViews()
        llClipboardTargetPills.addView(createPill("🌐 Tümü (Yayınla)", selectedClipboardHostKey == null) {
            selectedClipboardHostKey = null
            updateTargetHostSelectors()
        })
        for (host in connectedHosts) {
            val icon = if (host.os.lowercase() == "mac") "🍏" else "🪟"
            val isSel = selectedClipboardHostKey == host.key
            llClipboardTargetPills.addView(createPill("$icon ${host.name}", isSel) {
                selectedClipboardHostKey = host.key
                updateTargetHostSelectors()
            })
        }

        // --- 4. Files & Web Target Selector ---
        tvFilesSelectedHost.text = if (selectedFilesHostKey == null) {
            "🌐 Tüm Bilgisayarlar (${connectedHosts.size})"
        } else {
            val host = connectedHosts.find { it.key == selectedFilesHostKey }
            val icon = if (host?.os?.lowercase() == "mac") "🍏" else "🪟"
            "🎯 $icon ${host?.name ?: selectedFilesHostKey}"
        }
        llFilesTargetPills.removeAllViews()
        llFilesTargetPills.addView(createPill("🌐 Tümü (Yayınla)", selectedFilesHostKey == null) {
            selectedFilesHostKey = null
            updateTargetHostSelectors()
        })
        for (host in connectedHosts) {
            val icon = if (host.os.lowercase() == "mac") "🍏" else "🪟"
            val isSel = selectedFilesHostKey == host.key
            llFilesTargetPills.addView(createPill("$icon ${host.name}", isSel) {
                selectedFilesHostKey = host.key
                updateTargetHostSelectors()
            })
        }
    }

    private fun refreshMediaPills() {
        val wsClient = SyncForegroundService.instance?.webSocketClient
        val connectedHosts = wsClient?.connectedHosts?.filter { it.isConnected && it.isAuthorized } ?: emptyList()

        if (selectedMediaHostKey != null && connectedHosts.none { it.key == selectedMediaHostKey }) {
            selectedMediaHostKey = connectedHosts.firstOrNull()?.key
        }
        if (selectedMediaHostKey == null && connectedHosts.isNotEmpty()) {
            selectedMediaHostKey = connectedHosts.first().key
        }

        val curMediaHost = connectedHosts.find { it.key == selectedMediaHostKey }
        tvMediaSelectedHost.text = if (curMediaHost != null) {
            val icon = if (curMediaHost.os.lowercase() == "mac") "🍏" else "🪟"
            "🎯 $icon ${curMediaHost.name} (Denetleniyor)"
        } else {
            "🎯 Bilgisayar Bağlı Değil"
        }

        if (connectedHosts.isEmpty()) {
            llMediaTargetPills.removeAllViews()
            val emptyTv = TextView(this).apply {
                text = "Bağlı ve eşleşmiş bilgisayar yok"
                textSize = 11f
                setTextColor(ContextCompat.getColor(context, R.color.text_secondary))
            }
            llMediaTargetPills.addView(emptyTv)
            return
        }

        val count = llMediaTargetPills.childCount
        if (count != connectedHosts.size || (0 until count).any { llMediaTargetPills.getChildAt(it) !is TextView }) {
            llMediaTargetPills.removeAllViews()
            for (host in connectedHosts) {
                val pill = createMediaPill(host)
                llMediaTargetPills.addView(pill)
            }
        } else {
            for ((index, host) in connectedHosts.withIndex()) {
                val pill = llMediaTargetPills.getChildAt(index) as? TextView ?: continue
                updateMediaPillView(pill, host)
            }
        }
    }

    private fun createMediaPill(host: ConnectedHost): TextView {
        val tv = TextView(this)
        updateMediaPillView(tv, host)
        tv.setOnClickListener {
            it.performHapticFeedback(HapticFeedbackConstants.KEYBOARD_TAP)
            selectedMediaHostKey = host.key
            val info = host.lastMedia ?: MediaInfoPayload(title = "Medya Çalmıyor", artist = host.name)
            updateMediaUI(info)
            refreshMediaPills()
        }
        return tv
    }

    private fun updateMediaPillView(tv: TextView, host: ConnectedHost) {
        val icon = if (host.os.lowercase() == "mac") "🍏" else "🪟"
        val isSel = selectedMediaHostKey == host.key
        val media = host.lastMedia
        val playingIcon = if (media?.is_playing == true) " ▶" else if (media != null && media.title.isNotBlank()) " ⏸" else ""
        val trackShort = if (media != null && media.title.isNotBlank()) {
            val t = if (media.title.length > 14) media.title.substring(0, 12) + ".." else media.title
            " • $t"
        } else ""

        val label = "$icon ${host.name}$trackShort$playingIcon"
        tv.text = label
        tv.layoutParams = LinearLayout.LayoutParams(
            LinearLayout.LayoutParams.WRAP_CONTENT,
            dpToPx(34)
        ).apply {
            marginEnd = dpToPx(8)
        }
        tv.gravity = Gravity.CENTER
        tv.setPadding(dpToPx(14), 0, dpToPx(14), 0)
        tv.textSize = 12f
        tv.typeface = Typeface.DEFAULT_BOLD

        if (isSel) {
            tv.setBackgroundResource(R.drawable.tab_active_bg)
            tv.setTextColor(ContextCompat.getColor(this, R.color.accent_blue))
        } else {
            tv.setBackgroundResource(R.drawable.tab_inactive_bg)
            tv.setTextColor(ContextCompat.getColor(this, R.color.text_secondary))
        }
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
            updateTargetHostSelectors()
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
        updateTargetHostSelectors()
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

        // Contacts
        val contactsGranted = ContextCompat.checkSelfPermission(this, Manifest.permission.READ_CONTACTS) == PackageManager.PERMISSION_GRANTED
        if (contactsGranted) {
            tvContactsPermissionStatus.text = "İzin Verildi 🟢"
            tvContactsPermissionStatus.setTextColor(Color.parseColor("#3FB950"))
            btnGrantContacts.visibility = View.GONE
        } else {
            tvContactsPermissionStatus.text = "İzin Gerekli 🔴"
            tvContactsPermissionStatus.setTextColor(Color.parseColor("#F85149"))
            btnGrantContacts.visibility = View.VISIBLE
        }

        // Photos
        val photosGranted = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            ContextCompat.checkSelfPermission(this, Manifest.permission.READ_MEDIA_IMAGES) == PackageManager.PERMISSION_GRANTED
        } else {
            ContextCompat.checkSelfPermission(this, Manifest.permission.READ_EXTERNAL_STORAGE) == PackageManager.PERMISSION_GRANTED
        }
        if (photosGranted) {
            tvPhotosPermissionStatus.text = "İzin Verildi 🟢"
            tvPhotosPermissionStatus.setTextColor(Color.parseColor("#3FB950"))
            btnGrantPhotos.visibility = View.GONE
        } else {
            tvPhotosPermissionStatus.text = "İzin Gerekli 🔴"
            tvPhotosPermissionStatus.setTextColor(Color.parseColor("#F85149"))
            btnGrantPhotos.visibility = View.VISIBLE
        }
    }

    private fun requestAppPermissions() {
        val permissions = mutableListOf(
            Manifest.permission.READ_PHONE_STATE,
            Manifest.permission.READ_CALL_LOG,
            Manifest.permission.READ_SMS,
            Manifest.permission.SEND_SMS,
            Manifest.permission.RECEIVE_SMS,
            Manifest.permission.READ_CONTACTS,
            Manifest.permission.RECORD_AUDIO,
            Manifest.permission.ACCESS_FINE_LOCATION
        )
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            permissions.add(Manifest.permission.READ_MEDIA_IMAGES)
        } else {
            permissions.add(Manifest.permission.READ_EXTERNAL_STORAGE)
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            permissions.add(Manifest.permission.ANSWER_PHONE_CALLS)
            permissions.add(Manifest.permission.CALL_PHONE)
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
