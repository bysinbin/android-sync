package com.sync.android.service

import android.content.Context
import android.content.Intent
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.util.Log
import com.sync.android.model.HotspotStatusPayload
import java.net.NetworkInterface
import java.util.Collections

class HotspotManager(private val context: Context) {

    companion object {
        private const val TAG = "HotspotManager"
        var instance: HotspotManager? = null
    }

    init {
        instance = this
    }

    private val wifiManager = context.applicationContext.getSystemService(Context.WIFI_SERVICE) as? WifiManager
    private var hotspotReservation: Any? = null
    var isHotspotActive = false
        private set
    var currentSSID: String = ""
        private set
    var currentPassword: String = ""
        private set

    var onStatusChanged: ((HotspotStatusPayload) -> Unit)? = null

    fun startHotspot() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            try {
                if (wifiManager == null) {
                    Log.w(TAG, "WifiManager mevcut değil")
                    notifyStatus(false, "", "")
                    return
                }

                wifiManager.startLocalOnlyHotspot(object : WifiManager.LocalOnlyHotspotCallback() {
                    override fun onStarted(reservation: WifiManager.LocalOnlyHotspotReservation) {
                        super.onStarted(reservation)
                        hotspotReservation = reservation
                        isHotspotActive = true

                        var ssid = ""
                        var password = ""

                        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                            val config = reservation.softApConfiguration
                            ssid = config.ssid ?: "Android-Sync-Hotspot"
                            password = config.passphrase ?: ""
                        } else {
                            @Suppress("DEPRECATION")
                            val config = reservation.wifiConfiguration
                            ssid = config?.SSID ?: "Android-Sync-Hotspot"
                            password = config?.preSharedKey ?: ""
                        }

                        currentSSID = ssid
                        currentPassword = password
                        Log.d(TAG, "LocalOnlyHotspot başlatıldı: SSID=$ssid, Şifre=$password")
                        notifyStatus(true, ssid, password)
                    }

                    override fun onStopped() {
                        super.onStopped()
                        hotspotReservation = null
                        isHotspotActive = false
                        Log.d(TAG, "LocalOnlyHotspot durduruldu.")
                        notifyStatus(false, "", "")
                    }

                    override fun onFailed(reason: Int) {
                        super.onFailed(reason)
                        Log.e(TAG, "LocalOnlyHotspot başlatılamadı. Kod: $reason")
                        isHotspotActive = false
                        // Fallback: Open Tethering settings directly so user can enable standard hotspot
                        openTetheringSettings()
                        notifyStatus(false, "", "")
                    }
                }, Handler(Looper.getMainLooper()))
            } catch (e: Exception) {
                Log.e(TAG, "startLocalOnlyHotspot hatası: ${e.message}")
                openTetheringSettings()
                notifyStatus(false, "", "")
            }
        } else {
            openTetheringSettings()
            notifyStatus(false, "", "")
        }
    }

    fun stopHotspot() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val res = hotspotReservation as? WifiManager.LocalOnlyHotspotReservation
            try {
                res?.close()
            } catch (e: Exception) {
                Log.w(TAG, "Hotspot kapatma hatası: ${e.message}")
            }
            hotspotReservation = null
        }
        isHotspotActive = false
        currentSSID = ""
        currentPassword = ""
        notifyStatus(false, "", "")
    }

    fun getStatus(): HotspotStatusPayload {
        return HotspotStatusPayload(
            enabled = isHotspotActive,
            ssid = currentSSID,
            password = currentPassword,
            ip = getHotspotIpAddress()
        )
    }

    private fun openTetheringSettings() {
        try {
            val intent = Intent().apply {
                action = Intent.ACTION_MAIN
                setClassName("com.android.settings", "com.android.settings.TetherSettings")
                flags = Intent.FLAG_ACTIVITY_NEW_TASK
            }
            context.startActivity(intent)
        } catch (_: Exception) {
            try {
                val fallbackIntent = Intent(Settings.ACTION_WIRELESS_SETTINGS).apply {
                    flags = Intent.FLAG_ACTIVITY_NEW_TASK
                }
                context.startActivity(fallbackIntent)
            } catch (_: Exception) {}
        }
    }

    private fun notifyStatus(enabled: Boolean, ssid: String, pass: String) {
        val payload = HotspotStatusPayload(
            enabled = enabled,
            ssid = ssid,
            password = pass,
            ip = if (enabled) getHotspotIpAddress() else null
        )
        onStatusChanged?.invoke(payload)
    }

    private fun getHotspotIpAddress(): String {
        try {
            val interfaces = Collections.list(NetworkInterface.getNetworkInterfaces())
            for (intf in interfaces) {
                if (intf.name.contains("wlan") || intf.name.contains("ap0") || intf.name.contains("softap")) {
                    val addrs = Collections.list(intf.inetAddresses)
                    for (addr in addrs) {
                        if (!addr.isLoopbackAddress && addr.hostAddress?.indexOf(':') == -1) {
                            return addr.hostAddress ?: ""
                        }
                    }
                }
            }
        } catch (_: Exception) {}
        return "192.168.43.1"
    }
}
