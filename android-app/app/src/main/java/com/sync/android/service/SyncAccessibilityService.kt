package com.sync.android.service

import android.accessibilityservice.AccessibilityService
import android.accessibilityservice.GestureDescription
import android.graphics.Path
import android.os.Build
import android.util.Log
import android.view.accessibility.AccessibilityEvent

class SyncAccessibilityService : AccessibilityService() {

    companion object {
        private const val TAG = "SyncAccessibility"
        var instance: SyncAccessibilityService? = null
        val isRunning: Boolean get() = instance != null
    }

    override fun onServiceConnected() {
        super.onServiceConnected()
        instance = this
        Log.d(TAG, "SyncAccessibilityService bağlandı ve aktif!")
    }

    override fun onAccessibilityEvent(event: AccessibilityEvent?) {
        // Event dinleme ihtiyacımız yok, yalnızca jest ve global eylemler için
    }

    override fun onInterrupt() {
        Log.w(TAG, "SyncAccessibilityService kesintiye uğradı.")
    }

    override fun onDestroy() {
        super.onDestroy()
        if (instance == this) {
            instance = null
        }
        Log.d(TAG, "SyncAccessibilityService sonlandırıldı.")
    }

    /**
     * Ekranda belirtilen koordinata dokunma (Tap) simülasyonu yapar.
     */
    fun performTap(x: Float, y: Float): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.N) return false
        val path = Path().apply {
            moveTo(x, y)
        }
        val stroke = GestureDescription.StrokeDescription(path, 0, 50)
        val gesture = GestureDescription.Builder().addStroke(stroke).build()
        return dispatchGesture(gesture, null, null)
    }

    /**
     * Ekranda belirtilen iki koordinat arasında kaydırma (Swipe / Drag) simülasyonu yapar.
     */
    fun performSwipe(startX: Float, startY: Float, endX: Float, endY: Float, durationMs: Long = 250): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.N) return false
        val path = Path().apply {
            moveTo(startX, startY)
            lineTo(endX, endY)
        }
        val stroke = GestureDescription.StrokeDescription(path, 0, durationMs.coerceAtLeast(50))
        val gesture = GestureDescription.Builder().addStroke(stroke).build()
        return dispatchGesture(gesture, null, null)
    }

    // Global Gezinme Aksiyonları
    fun performBack(): Boolean = performGlobalAction(GLOBAL_ACTION_BACK)
    fun performHome(): Boolean = performGlobalAction(GLOBAL_ACTION_HOME)
    fun performRecents(): Boolean = performGlobalAction(GLOBAL_ACTION_RECENTS)
    fun performNotifications(): Boolean = performGlobalAction(GLOBAL_ACTION_NOTIFICATIONS)

    fun performLock(): Boolean {
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            performGlobalAction(GLOBAL_ACTION_LOCK_SCREEN)
        } else {
            false
        }
    }

    /**
     * Odaklanmış metin kutusuna doğrudan metin yazar veya shell input ile gönderir.
     */
    fun typeText(text: String): Boolean {
        try {
            val root = rootInActiveWindow
            val focusNode = root?.findFocus(android.view.accessibility.AccessibilityNodeInfo.FOCUS_INPUT)
            if (focusNode != null) {
                val current = focusNode.text?.toString() ?: ""
                val args = android.os.Bundle().apply {
                    putCharSequence(
                        android.view.accessibility.AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE,
                        current + text
                    )
                }
                if (focusNode.performAction(android.view.accessibility.AccessibilityNodeInfo.ACTION_SET_TEXT, args)) {
                    Log.d(TAG, "typeText AccessibilityNode ile yazıldı: $text")
                    return true
                }
            }
        } catch (e: Exception) {
            Log.w(TAG, "Accessibility findFocus hatası, shell fallback deneniyor", e)
        }

        // Shell fallback
        return try {
            val escaped = text.replace(" ", "%s").replace("\"", "\\\"").replace("&", "\\&")
            Runtime.getRuntime().exec(arrayOf("input", "text", escaped))
            Log.d(TAG, "typeText shell exec ile gönderildi: $text")
            true
        } catch (e: Exception) {
            Log.e(TAG, "typeText shell exec başarısız", e)
            false
        }
    }

    /**
     * Android tuş kodu (Enter = 66, Backspace = 67, vb.) simülasyonu yapar.
     */
    fun sendKey(keyCode: Int): Boolean {
        if (keyCode == 67) { // KEYCODE_DEL / Backspace
            try {
                val root = rootInActiveWindow
                val focusNode = root?.findFocus(android.view.accessibility.AccessibilityNodeInfo.FOCUS_INPUT)
                if (focusNode != null) {
                    val current = focusNode.text?.toString() ?: ""
                    if (current.isNotEmpty()) {
                        val args = android.os.Bundle().apply {
                            putCharSequence(
                                android.view.accessibility.AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE,
                                current.dropLast(1)
                            )
                        }
                        if (focusNode.performAction(android.view.accessibility.AccessibilityNodeInfo.ACTION_SET_TEXT, args)) {
                            return true
                        }
                    }
                }
            } catch (e: Exception) {
                Log.w(TAG, "sendKey Backspace node hatası", e)
            }
        }

        // Shell fallback for keyevent
        return try {
            Runtime.getRuntime().exec(arrayOf("input", "keyevent", keyCode.toString()))
            Log.d(TAG, "sendKey shell exec ile iletildi: $keyCode")
            true
        } catch (e: Exception) {
            Log.e(TAG, "sendKey başarısız", e)
            false
        }
    }
}
