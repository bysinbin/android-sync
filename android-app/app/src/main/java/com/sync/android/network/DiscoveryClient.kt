package com.sync.android.network

import android.content.Context
import android.net.wifi.WifiManager
import android.util.Log
import com.google.gson.Gson
import com.sync.android.model.DiscoveryPacket
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress

class DiscoveryClient(
    private val context: Context,
    private val onServerFound: (ip: String, port: Int, serverName: String) -> Unit
) {
    private val TAG = "DiscoveryClient"
    private val DISCOVERY_PORT = 42425
    private var isRunning = false
    private var socket: DatagramSocket? = null
    private var multicastLock: WifiManager.MulticastLock? = null

    fun start() {
        if (isRunning) return
        isRunning = true

        val wifi = context.applicationContext.getSystemService(Context.WIFI_SERVICE) as? WifiManager
        multicastLock = wifi?.createMulticastLock("sync_multicast")?.apply {
            setReferenceCounted(true)
            acquire()
        }

        Thread {
            try {
                socket = DatagramSocket(DISCOVERY_PORT).apply {
                    broadcast = true
                    soTimeout = 3000
                }
                Log.d(TAG, "Listening on UDP port $DISCOVERY_PORT")

                // Also send a discover request broadcast
                sendDiscoverRequest()

                val buffer = ByteArray(1024)
                while (isRunning) {
                    try {
                        val packet = DatagramPacket(buffer, buffer.size)
                        socket?.receive(packet)

                        val jsonStr = String(packet.data, 0, packet.length)
                        val hostIp = packet.address.hostAddress ?: continue
                        val parsed = Gson().fromJson(jsonStr, DiscoveryPacket::class.java)

                        if (parsed.type == "DISCOVER_BEACON" || parsed.type == "DISCOVER_RESPONSE") {
                            val name = parsed.server_name ?: "Mac"
                            val port = if (parsed.ws_port > 0) parsed.ws_port else 42424
                            Log.d(TAG, "Mac detected: $name at $hostIp:$port")
                            onServerFound(hostIp, port, name)
                        }
                    } catch (e: java.net.SocketTimeoutException) {
                        // Periodic re-broadcast if nothing found yet
                        sendDiscoverRequest()
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
        } catch (e: Exception) {
            // Ignore broadcast failure
        }
    }

    fun stop() {
        isRunning = false
        try {
            socket?.close()
            socket = null
            multicastLock?.release()
            multicastLock = null
        } catch (e: Exception) {
            // Ignored
        }
    }
}
