package com.sync.android.network

import android.content.Context
import android.net.wifi.WifiManager
import android.util.Log
import com.google.gson.Gson
import com.sync.android.model.DiscoveryPacket
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.Socket

class DiscoveryClient(
    private val context: Context,
    private val onServerFound: (ip: String, port: Int, serverName: String) -> Unit
) {
    private val TAG = "DiscoveryClient"
    private val DISCOVERY_PORT = 42425
    @Volatile private var isRunning = false
    private var socket: DatagramSocket? = null
    private var multicastLock: WifiManager.MulticastLock? = null

    fun start() {
        if (isRunning) return
        isRunning = true

        val wifi = context.applicationContext.getSystemService(Context.WIFI_SERVICE) as? WifiManager
        try {
            multicastLock = wifi?.createMulticastLock("sync_multicast")?.apply {
                setReferenceCounted(false)
                acquire()
            }
        } catch (e: Exception) {
            Log.w(TAG, "MulticastLock acquire error: ${e.message}")
        }

        // Active Probe Known Endpoints immediately in background
        probeKnownEndpoints()

        Thread {
            try {
                socket = DatagramSocket(null).apply {
                    reuseAddress = true
                    broadcast = true
                    soTimeout = 3000
                    bind(InetSocketAddress(DISCOVERY_PORT))
                }
                Log.d(TAG, "Listening on UDP port $DISCOVERY_PORT with reuseAddress")

                // Also send a discover request broadcast
                sendDiscoverRequest()

                val buffer = ByteArray(2048)
                while (isRunning) {
                    try {
                        val packet = DatagramPacket(buffer, buffer.size)
                        socket?.receive(packet)

                        val jsonStr = String(packet.data, 0, packet.length)
                        val hostIp = packet.address.hostAddress ?: continue
                        val parsed = Gson().fromJson(jsonStr, DiscoveryPacket::class.java)

                        if (parsed != null && (parsed.type == "DISCOVER_BEACON" || parsed.type == "DISCOVER_RESPONSE")) {
                            val name = parsed.server_name ?: "Mac"
                            val port = if (parsed.ws_port > 0) parsed.ws_port else 42424
                            Log.d(TAG, "Cihaz UDP ile tespit edildi: $name at $hostIp:$port")
                            onServerFound(hostIp, port, name)
                        }
                    } catch (e: java.net.SocketTimeoutException) {
                        // Periodic re-broadcast and probe
                        sendDiscoverRequest()
                        probeKnownEndpoints()
                    } catch (e: Exception) {
                        if (!isRunning) break
                        Log.e(TAG, "UDP receive error: ${e.message}")
                    }
                }
            } catch (e: Exception) {
                Log.e(TAG, "Discovery socket error: ${e.message}")
            } finally {
                stop()
            }
        }.start()
    }

    private fun sendDiscoverRequest() {
        try {
            val req = Gson().toJson(mapOf("type" to "DISCOVER_REQUEST"))
            val data = req.toByteArray()
            val broadcastAddr = InetAddress.getByName("255.255.255.255")
            val sendPacket = DatagramPacket(data, data.size, broadcastAddr, DISCOVERY_PORT)
            socket?.send(sendPacket)

            // Also send to local subnet broadcast
            try {
                val subnetBroadcast = InetAddress.getByName("192.168.50.255")
                val subnetPacket = DatagramPacket(data, data.size, subnetBroadcast, DISCOVERY_PORT)
                socket?.send(subnetPacket)
            } catch (_: Exception) {}
        } catch (e: Exception) {
            // Ignore broadcast failure
        }
    }

    fun probeKnownEndpoints() {
        Thread {
            val candidates = listOf("192.168.50.96", "192.168.50.97", "10.0.2.2")
            for (candidateIp in candidates) {
                try {
                    val s = Socket()
                    s.connect(InetSocketAddress(candidateIp, 42424), 800)
                    s.close()
                    // Port is open! Determine server name
                    val isMac = candidateIp == "192.168.50.96"
                    val hostName = if (isMac) "ferit-MacBook-Pro-2.local (Mac Sync)" else "DESKTOP-VBB5GUA (Windows Sync)"
                    Log.d(TAG, "Aktif TCP taramasında cihaz bulundu: $hostName ($candidateIp:42424)")
                    onServerFound(candidateIp, 42424, hostName)
                } catch (_: Exception) {
                    // Not reachable on this IP
                }
            }
        }.start()
    }

    fun stop() {
        isRunning = false
        try {
            socket?.close()
            socket = null
        } catch (_: Exception) {}

        try {
            if (multicastLock?.isHeld == true) {
                multicastLock?.release()
            }
            multicastLock = null
        } catch (_: Exception) {}
    }
}
