package com.sync.android.service

import android.content.ComponentName
import android.content.Context
import android.media.AudioManager
import android.media.Ringtone
import android.media.RingtoneManager
import android.media.session.MediaSessionManager
import android.media.session.PlaybackState
import android.os.Build
import android.os.VibrationEffect
import android.os.Vibrator
import android.os.VibratorManager
import android.util.Log
import android.view.KeyEvent

object PhoneController {
    private const val TAG = "PhoneController"
    private var activeRingtone: Ringtone? = null
    private var activeVibrator: Vibrator? = null

    fun handleAction(context: Context, action: String) {
        Log.d(TAG, "Gelen telefon kontrol komutu: $action")
        when (action.uppercase()) {
            "PLAY_PAUSE", "TOGGLE", "MEDIA_PLAY_PAUSE" -> {
                if (!controlActiveSession(context, "PLAY_PAUSE")) {
                    dispatchMediaKey(context, KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE)
                }
            }
            "PLAY", "MEDIA_PLAY" -> {
                if (!controlActiveSession(context, "PLAY")) {
                    dispatchMediaKey(context, KeyEvent.KEYCODE_MEDIA_PLAY)
                }
            }
            "PAUSE", "MEDIA_PAUSE" -> {
                if (!controlActiveSession(context, "PAUSE")) {
                    dispatchMediaKey(context, KeyEvent.KEYCODE_MEDIA_PAUSE)
                }
            }
            "NEXT", "MEDIA_NEXT" -> {
                if (!controlActiveSession(context, "NEXT")) {
                    dispatchMediaKey(context, KeyEvent.KEYCODE_MEDIA_NEXT)
                }
            }
            "PREVIOUS", "PREV", "MEDIA_PREV", "MEDIA_PREVIOUS" -> {
                if (!controlActiveSession(context, "PREVIOUS")) {
                    dispatchMediaKey(context, KeyEvent.KEYCODE_MEDIA_PREVIOUS)
                }
            }
            "SEEK_FORWARD", "FORWARD", "FORWARD_15", "MEDIA_FORWARD_15", "MEDIA_FORWARD" -> {
                if (!seekActiveSession(context, 15000)) {
                    dispatchMediaKey(context, KeyEvent.KEYCODE_MEDIA_FAST_FORWARD)
                }
            }
            "SEEK_BACKWARD", "REWIND", "REWIND_15", "MEDIA_REWIND_15", "MEDIA_REWIND" -> {
                if (!seekActiveSession(context, -15000)) {
                    dispatchMediaKey(context, KeyEvent.KEYCODE_MEDIA_REWIND)
                }
            }
            "VOLUME_UP", "VOLUP" -> {
                val audioManager = context.getSystemService(Context.AUDIO_SERVICE) as AudioManager
                audioManager.adjustStreamVolume(AudioManager.STREAM_MUSIC, AudioManager.ADJUST_RAISE, AudioManager.FLAG_SHOW_UI)
            }
            "VOLUME_DOWN", "VOLDOWN" -> {
                val audioManager = context.getSystemService(Context.AUDIO_SERVICE) as AudioManager
                audioManager.adjustStreamVolume(AudioManager.STREAM_MUSIC, AudioManager.ADJUST_LOWER, AudioManager.FLAG_SHOW_UI)
            }
            "MUTE" -> {
                val audioManager = context.getSystemService(Context.AUDIO_SERVICE) as AudioManager
                audioManager.adjustStreamVolume(AudioManager.STREAM_MUSIC, AudioManager.ADJUST_TOGGLE_MUTE, AudioManager.FLAG_SHOW_UI)
            }
            "RING", "FIND_PHONE" -> {
                startRinging(context)
            }
            "STOP_RING" -> {
                stopRinging()
            }
            else -> {
                if (action.uppercase().startsWith("SEEK_PERCENT")) {
                    val percentStr = action.substringAfter(":", "").ifEmpty { action.substringAfter("percent=", "") }
                    val p = percentStr.toFloatOrNull() ?: 0f
                    seekToPercent(context, p)
                } else {
                    Log.w(TAG, "Bilinmeyen komut: $action")
                }
            }
        }
    }

    private fun findActiveMediaController(context: Context): android.media.session.MediaController? {
        return try {
            val sessionManager = context.getSystemService(Context.MEDIA_SESSION_SERVICE) as? MediaSessionManager
            val componentName = ComponentName(context, SyncNotificationListenerService::class.java)
            val controllers = sessionManager?.getActiveSessions(componentName) ?: return null
            if (controllers.isEmpty()) return null

            // 1. Önce aktif çalan session'ı bul
            for (c in controllers) {
                if (c.playbackState?.state == PlaybackState.STATE_PLAYING) {
                    return c
                }
            }
            // 2. Çalan yoksa metadata'sı olan ilk session
            for (c in controllers) {
                val title = c.metadata?.getString(android.media.MediaMetadata.METADATA_KEY_TITLE)
                if (!title.isNullOrEmpty()) {
                    return c
                }
            }
            // 3. Fallback ilk controller
            controllers[0]
        } catch (e: Exception) {
            Log.e(TAG, "findActiveMediaController hatası: ${e.message}")
            null
        }
    }

    private fun controlActiveSession(context: Context, action: String): Boolean {
        val controller = findActiveMediaController(context) ?: return false
        return try {
            val transport = controller.transportControls
            when (action) {
                "PLAY_PAUSE" -> {
                    val state = controller.playbackState?.state
                    if (state == PlaybackState.STATE_PLAYING) {
                        transport.pause()
                    } else {
                        transport.play()
                    }
                }
                "PLAY" -> transport.play()
                "PAUSE" -> transport.pause()
                "NEXT" -> transport.skipToNext()
                "PREVIOUS" -> transport.skipToPrevious()
            }
            true
        } catch (e: SecurityException) {
            Log.d(TAG, "NotificationListener izni yok, fallback yapılıyor")
            false
        } catch (e: Exception) {
            Log.e(TAG, "MediaSession kontrol hatası: ${e.message}")
            false
        }
    }

    private fun seekActiveSession(context: Context, deltaMs: Long): Boolean {
        val controller = findActiveMediaController(context) ?: return false
        return try {
            val current = controller.playbackState?.position ?: 0L
            val duration = controller.metadata?.getLong(android.media.MediaMetadata.METADATA_KEY_DURATION) ?: 0L
            var target = current + deltaMs
            if (target < 0) target = 0
            if (duration > 0 && target > duration) target = duration
            controller.transportControls.seekTo(target)
            Log.d(TAG, "Telefonda medya süresi kaydırıldı (delta: ${deltaMs}ms) -> $target / $duration ms")
            true
        } catch (e: Exception) {
            Log.e(TAG, "seekActiveSession hatası: ${e.message}")
            false
        }
    }

    fun seekToPercent(context: Context, percent: Float): Boolean {
        val controller = findActiveMediaController(context) ?: return false
        return try {
            val duration = controller.metadata?.getLong(android.media.MediaMetadata.METADATA_KEY_DURATION) ?: 0L
            val targetMs = if (duration > 0) {
                (duration * (percent.coerceIn(0f, 100f) / 100f)).toLong()
            } else {
                0L
            }
            controller.transportControls.seekTo(targetMs)
            Log.d(TAG, "Telefonda süre kaydırıldı: %$percent -> $targetMs ms")
            true
        } catch (e: Exception) {
            Log.e(TAG, "seekToPercent hatası: ${e.message}")
            false
        }
    }

    fun getCurrentMedia(context: Context): com.sync.android.model.MediaInfoPayload? {
        return try {
            val sessionManager = context.getSystemService(Context.MEDIA_SESSION_SERVICE) as? MediaSessionManager
            val componentName = ComponentName(context, SyncNotificationListenerService::class.java)
            val controllers = sessionManager?.getActiveSessions(componentName)
            if (!controllers.isNullOrEmpty()) {
                var bestCandidate: com.sync.android.model.MediaInfoPayload? = null
                for (controller in controllers) {
                    val metadata = controller.metadata
                    val state = controller.playbackState
                    val title = metadata?.getString(android.media.MediaMetadata.METADATA_KEY_TITLE)
                        ?: metadata?.getString(android.media.MediaMetadata.METADATA_KEY_DISPLAY_TITLE)
                        ?: ""
                    val artist = metadata?.getString(android.media.MediaMetadata.METADATA_KEY_ARTIST)
                        ?: metadata?.getString(android.media.MediaMetadata.METADATA_KEY_AUTHOR)
                        ?: ""
                    val album = metadata?.getString(android.media.MediaMetadata.METADATA_KEY_ALBUM) ?: ""
                    val duration = metadata?.getLong(android.media.MediaMetadata.METADATA_KEY_DURATION) ?: 0L
                    val position = state?.position ?: 0L
                    val isPlaying = state?.state == PlaybackState.STATE_PLAYING
                    val percent = if (duration > 0) (position.toDouble() / duration.toDouble()) * 100.0 else 0.0

                    if (title.isNotEmpty()) {
                        val payload = com.sync.android.model.MediaInfoPayload(
                            source = "phone",
                            title = title,
                            artist = artist,
                            album = album,
                            is_playing = isPlaying,
                            position_ms = position,
                            duration_ms = duration,
                            percent = percent
                        )
                        if (isPlaying) {
                            return payload
                        } else if (bestCandidate == null) {
                            bestCandidate = payload
                        }
                    }
                }
                bestCandidate
            } else null
        } catch (e: Exception) {
            null
        }
    }

    private fun dispatchMediaKey(context: Context, keyCode: Int) {
        val audioManager = context.getSystemService(Context.AUDIO_SERVICE) as AudioManager
        audioManager.dispatchMediaKeyEvent(KeyEvent(KeyEvent.ACTION_DOWN, keyCode))
        audioManager.dispatchMediaKeyEvent(KeyEvent(KeyEvent.ACTION_UP, keyCode))
    }

    private fun startRinging(context: Context) {
        stopRinging()
        try {
            val uri = RingtoneManager.getDefaultUri(RingtoneManager.TYPE_ALARM)
                ?: RingtoneManager.getDefaultUri(RingtoneManager.TYPE_RINGTONE)
            val ringtone = RingtoneManager.getRingtone(context.applicationContext, uri)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
                ringtone?.isLooping = true
            }
            ringtone?.play()
            activeRingtone = ringtone

            val v = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                val vm = context.getSystemService(Context.VIBRATOR_MANAGER_SERVICE) as? VibratorManager
                vm?.defaultVibrator
            } else {
                @Suppress("DEPRECATION")
                context.getSystemService(Context.VIBRATOR_SERVICE) as? Vibrator
            }
            activeVibrator = v
            val pattern = longArrayOf(0, 800, 400, 800, 400)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                v?.vibrate(VibrationEffect.createWaveform(pattern, 0))
            } else {
                @Suppress("DEPRECATION")
                v?.vibrate(pattern, 0)
            }
        } catch (e: Exception) {
            Log.e(TAG, "Telefon çaldırma hatası: ${e.message}")
        }
    }

    fun stopRinging() {
        try {
            activeRingtone?.stop()
            activeRingtone = null
            activeVibrator?.cancel()
            activeVibrator = null
        } catch (e: Exception) {
            Log.e(TAG, "Alarm durdurma hatası: ${e.message}")
        }
    }
}
