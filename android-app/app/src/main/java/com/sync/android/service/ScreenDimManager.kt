package com.sync.android.service

import android.content.Context
import android.graphics.Color
import android.graphics.PixelFormat
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.util.Log
import android.view.View
import android.view.WindowManager

class ScreenDimManager(private val context: Context) {

    companion object {
        private const val TAG = "ScreenDimManager"
        @Volatile
        var isDimmed: Boolean = false
            private set
    }

    private var overlayView: View? = null
    private val windowManager: WindowManager =
        context.getSystemService(Context.WINDOW_SERVICE) as WindowManager
    private val mainHandler = Handler(Looper.getMainLooper())

    fun setDimmed(enabled: Boolean) {
        mainHandler.post {
            try {
                if (enabled) {
                    if (isDimmed) return@post
                    enableDimming()
                } else {
                    if (!isDimmed) return@post
                    disableDimming()
                }
            } catch (e: Exception) {
                Log.e(TAG, "ScreenDim state değişimi hatası: enabled=$enabled", e)
            }
        }
    }

    private fun enableDimming() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M && Settings.canDrawOverlays(context)) {
            val layoutType = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY
            } else {
                @Suppress("DEPRECATION")
                WindowManager.LayoutParams.TYPE_PHONE
            }

            val params = WindowManager.LayoutParams(
                WindowManager.LayoutParams.MATCH_PARENT,
                WindowManager.LayoutParams.MATCH_PARENT,
                layoutType,
                WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE or
                        WindowManager.LayoutParams.FLAG_NOT_TOUCHABLE or
                        WindowManager.LayoutParams.FLAG_LAYOUT_IN_SCREEN or
                        WindowManager.LayoutParams.FLAG_FULLSCREEN,
                PixelFormat.TRANSLUCENT
            ).apply {
                screenBrightness = 0.0f
            }

            overlayView = View(context).apply {
                setBackgroundColor(Color.BLACK)
            }

            windowManager.addView(overlayView, params)
            isDimmed = true
            Log.d(TAG, "Ekran karartma aktif (AMOLED Siyah Overlay)")
        } else {
            // Shell fallback ile parlaklığı 1'e indir
            try {
                Runtime.getRuntime().exec(arrayOf("settings", "put", "system", "screen_brightness", "1"))
                isDimmed = true
                Log.d(TAG, "Ekran parlaklığı shell ile minimuma indirildi")
            } catch (e: Exception) {
                Log.w(TAG, "Shell parlaklık ayarı başarısız", e)
            }
        }
    }

    private fun disableDimming() {
        overlayView?.let {
            try {
                windowManager.removeView(it)
            } catch (e: Exception) {
                Log.w(TAG, "Overlay view kaldırma hatası", e)
            }
            overlayView = null
        }
        isDimmed = false

        // Shell fallback ile parlaklığı normale al
        try {
            Runtime.getRuntime().exec(arrayOf("settings", "put", "system", "screen_brightness", "128"))
        } catch (_: Exception) {}

        Log.d(TAG, "Ekran karartma kapatıldı, normal görünüme dönüldü")
    }
}
