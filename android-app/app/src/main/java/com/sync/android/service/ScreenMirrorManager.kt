package com.sync.android.service

import android.content.Context
import android.graphics.Bitmap
import android.graphics.PixelFormat
import android.hardware.display.DisplayManager
import android.hardware.display.VirtualDisplay
import android.media.Image
import android.media.ImageReader
import android.media.projection.MediaProjection
import android.os.Handler
import android.os.HandlerThread
import android.os.Looper
import android.util.Base64
import android.util.DisplayMetrics
import android.util.Log
import android.view.WindowManager
import com.sync.android.model.ProtocolEvents
import com.sync.android.model.ScreenMirrorFramePayload
import com.sync.android.model.toSyncMessage
import java.io.ByteArrayOutputStream
import java.io.OutputStream
import java.net.ServerSocket
import java.net.Socket
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicBoolean

class ScreenMirrorManager(private val context: Context) {

    companion object {
        private const val TAG = "ScreenMirrorManager"
        const val MJPEG_PORT = 8087
        var instance: ScreenMirrorManager? = null
    }

    init {
        instance = this
    }

    private var mediaProjection: MediaProjection? = null
    private var virtualDisplay: VirtualDisplay? = null
    private var imageReader: ImageReader? = null
    private var captureThread: HandlerThread? = null
    private var captureHandler: Handler? = null

    private var screenWidth = 720
    private var screenHeight = 1280
    private var screenDpi = 240
    private var isMirroring = AtomicBoolean(false)
    private var lastFrameTime = 0L
    private val targetFpsInterval = 1000L / 30L // ~30 FPS

    // HTTP MJPEG Streaming
    private var mjpegServer: ServerSocket? = null
    private val mjpegClients = CopyOnWriteArrayList<Socket>()
    private var mjpegServerRunning = false

    var onFrameEncoded: ((String, Int, Int) -> Unit)? = null

    fun setMediaProjection(mp: MediaProjection) {
        this.mediaProjection = mp
    }

    fun isMediaProjectionReady(): Boolean = mediaProjection != null

    fun startMirroring(quality: Int = 65) {
        if (isMirroring.get()) return
        val mp = mediaProjection
        if (mp == null) {
            Log.w(TAG, "MediaProjection henüz yetkilendirilmedi!")
            return
        }

        try {
            val wm = context.getSystemService(Context.WINDOW_SERVICE) as WindowManager
            val metrics = DisplayMetrics()
            @Suppress("DEPRECATION")
            wm.defaultDisplay.getRealMetrics(metrics)

            screenDpi = metrics.densityDpi
            // Scale down for ultra fast network streaming
            val scale = 0.5f.coerceAtLeast(720f / metrics.widthPixels.toFloat())
            screenWidth = (metrics.widthPixels * scale).toInt() and 1.inv() // make even
            screenHeight = (metrics.heightPixels * scale).toInt() and 1.inv()

            captureThread = HandlerThread("ScreenCaptureThread").apply { start() }
            captureHandler = Handler(captureThread!!.looper)

            // Android 14+ Zorunluluğu: VirtualDisplay oluşturulmadan önce Callback kaydedilmelidir
            try {
                mp.registerCallback(object : MediaProjection.Callback() {
                    override fun onStop() {
                        Log.d(TAG, "MediaProjection sistem tarafından sonlandırıldı.")
                        stopMirroring()
                    }
                }, captureHandler)
            } catch (e: Exception) {
                Log.w(TAG, "MediaProjection.registerCallback uyarısı: ${e.message}")
            }

            imageReader = ImageReader.newInstance(screenWidth, screenHeight, PixelFormat.RGBA_8888, 3)
            virtualDisplay = mp.createVirtualDisplay(
                "SyncMirror",
                screenWidth,
                screenHeight,
                screenDpi,
                DisplayManager.VIRTUAL_DISPLAY_FLAG_AUTO_MIRROR,
                imageReader!!.surface,
                null,
                captureHandler
            )

            imageReader!!.setOnImageAvailableListener({ reader ->
                val now = System.currentTimeMillis()
                if (now - lastFrameTime < targetFpsInterval) {
                    val discard = reader.acquireLatestImage()
                    discard?.close()
                    return@setOnImageAvailableListener
                }
                lastFrameTime = now

                var image: Image? = null
                try {
                    image = reader.acquireLatestImage() ?: return@setOnImageAvailableListener
                    val planes = image.planes
                    val buffer = planes[0].buffer
                    val pixelStride = planes[0].pixelStride
                    val rowStride = planes[0].rowStride
                    val rowPadding = rowStride - pixelStride * screenWidth

                    val bitmap = Bitmap.createBitmap(
                        screenWidth + rowPadding / pixelStride,
                        screenHeight,
                        Bitmap.Config.ARGB_8888
                    )
                    bitmap.copyPixelsFromBuffer(buffer)

                    // Crop to exact size
                    val croppedBitmap = if (rowPadding != 0) {
                        Bitmap.createBitmap(bitmap, 0, 0, screenWidth, screenHeight)
                    } else {
                        bitmap
                    }

                    val baos = ByteArrayOutputStream()
                    croppedBitmap.compress(Bitmap.CompressFormat.JPEG, quality, baos)
                    val jpegBytes = baos.toByteArray()

                    if (croppedBitmap != bitmap) {
                        croppedBitmap.recycle()
                    }
                    bitmap.recycle()

                    // 1. Broadcast to MJPEG HTTP clients
                    broadcastMjpegFrame(jpegBytes)

                    // 2. Deliver base64 to WebSocket clients
                    val b64 = Base64.encodeToString(jpegBytes, Base64.NO_WRAP)
                    onFrameEncoded?.invoke(b64, screenWidth, screenHeight)

                } catch (e: Exception) {
                    // Frame drop is normal under heavy load
                } finally {
                    image?.close()
                }
            }, captureHandler)

            isMirroring.set(true)
            startMjpegServer()
            Log.d(TAG, "Ekran yansıtma başlatıldı: ${screenWidth}x${screenHeight} @ ${screenDpi}dpi")
        } catch (e: Exception) {
            Log.e(TAG, "Ekran yansıtma başlatılamadı: ${e.message}", e)
        }
    }

    fun stopMirroring() {
        if (!isMirroring.getAndSet(false)) return
        try {
            virtualDisplay?.release()
            virtualDisplay = null
            imageReader?.close()
            imageReader = null
            captureThread?.quitSafely()
            captureThread = null
            captureHandler = null
            stopMjpegServer()
            SyncForegroundService.instance?.updateForegroundTypeForScreenMirror(false)
            Log.d(TAG, "Ekran yansıtma durduruldu.")
        } catch (e: Exception) {
            Log.e(TAG, "Ekran yansıtma durdurma hatası: ${e.message}")
        }
    }

    // Remote Touch & Gesture Injection
    fun injectTouch(action: String, normX: Float, normY: Float) {
        try {
            val wm = context.getSystemService(Context.WINDOW_SERVICE) as WindowManager
            val metrics = DisplayMetrics()
            @Suppress("DEPRECATION")
            wm.defaultDisplay.getRealMetrics(metrics)

            val px = (normX * metrics.widthPixels).toInt().coerceIn(0, metrics.widthPixels)
            val py = (normY * metrics.heightPixels).toInt().coerceIn(0, metrics.heightPixels)

            val a11y = SyncAccessibilityService.instance
            when (action.lowercase()) {
                "down", "tap", "click" -> {
                    if (a11y != null && a11y.performTap(px.toFloat(), py.toFloat())) {
                        Log.d(TAG, "Erişilebilirlik ile dokunma iletildi: ($px, $py)")
                    } else {
                        Thread {
                            try {
                                Runtime.getRuntime().exec(arrayOf("input", "tap", px.toString(), py.toString()))
                            } catch (e: Exception) {
                                Log.w(TAG, "input tap exec hatası: ${e.message}")
                            }
                        }.start()
                    }
                }
                "swipe_up" -> {
                    val startY = metrics.heightPixels * 0.75f
                    val endY = metrics.heightPixels * 0.25f
                    if (a11y != null && a11y.performSwipe(px.toFloat(), startY, px.toFloat(), endY, 250)) {
                        Log.d(TAG, "Erişilebilirlik ile yukarı kaydırma iletildi")
                    } else {
                        Thread { Runtime.getRuntime().exec(arrayOf("input", "swipe", px.toString(), startY.toInt().toString(), px.toString(), endY.toInt().toString(), "250")) }.start()
                    }
                }
                "swipe_down" -> {
                    val startY = metrics.heightPixels * 0.25f
                    val endY = metrics.heightPixels * 0.75f
                    if (a11y != null && a11y.performSwipe(px.toFloat(), startY, px.toFloat(), endY, 250)) {
                        Log.d(TAG, "Erişilebilirlik ile aşağı kaydırma iletildi")
                    } else {
                        Thread { Runtime.getRuntime().exec(arrayOf("input", "swipe", px.toString(), startY.toInt().toString(), px.toString(), endY.toInt().toString(), "250")) }.start()
                    }
                }
                "back" -> {
                    if (a11y == null || !a11y.performBack()) {
                        Thread { Runtime.getRuntime().exec(arrayOf("input", "keyevent", "4")) }.start()
                    }
                }
                "home" -> {
                    if (a11y == null || !a11y.performHome()) {
                        Thread { Runtime.getRuntime().exec(arrayOf("input", "keyevent", "3")) }.start()
                    }
                }
                "recents", "app_switch" -> {
                    if (a11y == null || !a11y.performRecents()) {
                        Thread { Runtime.getRuntime().exec(arrayOf("input", "keyevent", "187")) }.start()
                    }
                }
                "notifications" -> {
                    if (a11y == null || !a11y.performNotifications()) {
                        Thread { Runtime.getRuntime().exec(arrayOf("cmd", "statusbar", "expand-notifications")) }.start()
                    }
                }
                "power", "lock" -> {
                    if (a11y == null || !a11y.performLock()) {
                        Thread { Runtime.getRuntime().exec(arrayOf("input", "keyevent", "26")) }.start()
                    }
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "injectTouch hatası: ${e.message}")
        }
    }

    // Embedded HTTP MJPEG Server for seamless zero-lag streaming
    private fun startMjpegServer() {
        if (mjpegServerRunning) return
        mjpegServerRunning = true
        Thread {
            try {
                mjpegServer = ServerSocket(MJPEG_PORT)
                Log.d(TAG, "MJPEG Sunucusu başlatıldı: port $MJPEG_PORT")
                while (mjpegServerRunning && mjpegServer?.isClosed == false) {
                    val client = mjpegServer?.accept() ?: break
                    handleMjpegClient(client)
                }
            } catch (e: Exception) {
                if (mjpegServerRunning) {
                    Log.w(TAG, "MJPEG ServerSocket: ${e.message}")
                }
            }
        }.start()
    }

    private fun handleMjpegClient(socket: Socket) {
        Thread {
            try {
                val input = socket.getInputStream().bufferedReader()
                val line = input.readLine() ?: return@Thread
                if (!line.startsWith("GET")) {
                    socket.close()
                    return@Thread
                }

                val out = socket.getOutputStream()
                val header = ("HTTP/1.1 200 OK\r\n" +
                        "Content-Type: multipart/x-mixed-replace; boundary=--myboundary\r\n" +
                        "Cache-Control: no-cache\r\n" +
                        "Connection: close\r\n" +
                        "Access-Control-Allow-Origin: *\r\n\r\n").toByteArray()
                out.write(header)
                out.flush()

                mjpegClients.add(socket)
            } catch (e: Exception) {
                try { socket.close() } catch (_: Exception) {}
            }
        }.start()
    }

    private fun broadcastMjpegFrame(jpegBytes: ByteArray) {
        if (mjpegClients.isEmpty()) return
        val boundary = "--myboundary\r\nContent-Type: image/jpeg\r\nContent-Length: ${jpegBytes.size}\r\n\r\n".toByteArray()
        val tail = "\r\n".toByteArray()

        val dead = mutableListOf<Socket>()
        for (client in mjpegClients) {
            try {
                val out = client.getOutputStream()
                out.write(boundary)
                out.write(jpegBytes)
                out.write(tail)
                out.flush()
            } catch (e: Exception) {
                dead.add(client)
            }
        }
        for (d in dead) {
            mjpegClients.remove(d)
            try { d.close() } catch (_: Exception) {}
        }
    }

    private fun stopMjpegServer() {
        mjpegServerRunning = false
        try {
            for (c in mjpegClients) {
                try { c.close() } catch (_: Exception) {}
            }
            mjpegClients.clear()
            mjpegServer?.close()
            mjpegServer = null
        } catch (_: Exception) {}
    }
}
