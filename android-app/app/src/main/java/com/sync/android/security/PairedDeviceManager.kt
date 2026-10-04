package com.sync.android.security

import android.content.Context
import android.content.SharedPreferences
import com.google.gson.Gson
import com.google.gson.reflect.TypeToken
import java.util.UUID

data class PairedHostConfig(
    val clientId: String,
    var name: String,
    val os: String, // "windows" or "mac"
    var authToken: String,
    var isPaired: Boolean = true,
    var allowClipboard: Boolean = true,
    var allowCalls: Boolean = true,
    var allowSms: Boolean = true,
    var allowMedia: Boolean = true,
    var lastSeen: Long = System.currentTimeMillis(),
    var lastIp: String = "",
    var lastPort: Int = 42424
)

class PairedDeviceManager private constructor(context: Context) {
    private val prefs: SharedPreferences = context.getSharedPreferences("paired_devices_prefs", Context.MODE_PRIVATE)
    private val gson = Gson()

    companion object {
        @Volatile
        private var instance: PairedDeviceManager? = null

        fun getInstance(context: Context): PairedDeviceManager {
            return instance ?: synchronized(this) {
                instance ?: PairedDeviceManager(context.applicationContext).also { instance = it }
            }
        }
    }

    var meshClipboardEnabled: Boolean
        get() = prefs.getBoolean("mesh_clipboard", true)
        set(value) = prefs.edit().putBoolean("mesh_clipboard", value).apply()

    var ringAllDevicesOnCall: Boolean
        get() = prefs.getBoolean("ring_all_devices", true)
        set(value) = prefs.edit().putBoolean("ring_all_devices", value).apply()

    fun isAuthorized(clientId: String, token: String?): Boolean {
        if (clientId.isEmpty() || token.isNullOrEmpty()) return false
        val device = getDevice(clientId) ?: return false
        return device.isPaired && device.authToken == token
    }

    fun getDevice(clientId: String): PairedHostConfig? {
        val map = loadDevicesMap()
        return map[clientId]
    }

    fun getAllDevices(): List<PairedHostConfig> {
        return loadDevicesMap().values.toList().sortedByDescending { it.lastSeen }
    }

    fun saveDevice(device: PairedHostConfig) {
        val map = loadDevicesMap().toMutableMap()
        map[device.clientId] = device
        saveDevicesMap(map)
    }

    fun unpair(clientId: String) {
        val map = loadDevicesMap().toMutableMap()
        map.remove(clientId)
        saveDevicesMap(map)
    }

    fun updatePermissions(
        clientId: String,
        allowClipboard: Boolean,
        allowCalls: Boolean,
        allowSms: Boolean,
        allowMedia: Boolean
    ) {
        val device = getDevice(clientId) ?: return
        device.allowClipboard = allowClipboard
        device.allowCalls = allowCalls
        device.allowSms = allowSms
        device.allowMedia = allowMedia
        saveDevice(device)
    }

    fun generateAuthToken(): String {
        return UUID.randomUUID().toString().replace("-", "")
    }

    private fun loadDevicesMap(): Map<String, PairedHostConfig> {
        val json = prefs.getString("paired_hosts_json", null) ?: return emptyMap()
        return try {
            val type = object : TypeToken<Map<String, PairedHostConfig>>() {}.type
            gson.fromJson(json, type) ?: emptyMap()
        } catch (e: Exception) {
            emptyMap()
        }
    }

    private fun saveDevicesMap(map: Map<String, PairedHostConfig>) {
        val json = gson.toJson(map)
        prefs.edit().putString("paired_hosts_json", json).apply()
    }
}
