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
            "PLAY_PAUSE", "TOGGLE" -> {
                if (!controlActiveSession(context, "PLAY_PAUSE")) {
                    dispatchMediaKey(context, KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE)
                }
            }
            "PLAY" -> {
                if (!controlActiveSession(context, "PLAY")) {
                    dispatchMediaKey(context, KeyEvent.KEYCODE_MEDIA_PLAY)
                }
            }
            "PAUSE" -> {
                if (!controlActiveSession(context, "PAUSE")) {
                    dispatchMediaKey(context, KeyEvent.KEYCODE_MEDIA_PAUSE)
                }
            }
            "NEXT" -> {
                if (!controlActiveSession(context, "NEXT")) {
                    dispatchMediaKey(context, KeyEvent.KEYCODE_MEDIA_NEXT)
                }
            }
            "PREVIOUS", "PREV" -> {
                if (!controlActiveSession(context, "PREVIOUS")) {
                    dispatchMediaKey(context, KeyEvent.KEYCODE_MEDIA_PREVIOUS)
                }
            }
            "SEEK_FORWARD", "FORWARD", "FORWARD_15" -> {
                if (!seekActiveSession(context, 15000)) {
                    dispatchMediaKey(context, KeyEvent.KEYCODE_MEDIA_FAST_FORWARD)
                }
            }
            "SEEK_BACKWARD", "REWIND", "REWIND_15" -> {
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

    private fun controlActiveSession(context: Context, action: String): Boolean {
        return try {
            val sessionManager = context.getSystemService(Context.MEDIA_SESSION_SERVICE) as? MediaSessionManager
            val componentName = ComponentName(context, SyncNotificationListenerService::class.java)
            val controllers = sessionManager?.getActiveSessions(componentName)
            if (!controllers.isNullOrEmpty()) {
                val controller = controllers[0]
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
            } else {
                false
            }
        } catch (e: SecurityException) {
            Log.d(TAG, "NotificationListener izni yok, fallback yapılıyor")
            false
        } catch (e: Exception) {
            Log.e(TAG, "MediaSession kontrol hatası: ${e.message}")
            false
        }
    }

    private fun seekActiveSession(context: Context, deltaMs: Long): Boolean {
        return try {
            val sessionManager = context.getSystemService(Context.MEDIA_SESSION_SERVICE) as? MediaSessionManager
            val componentName = ComponentName(context, SyncNotificationListenerService::class.java)
            val controllers = sessionManager?.getActiveSessions(componentName)
            if (!controllers.isNullOrEmpty()) {
                val controller = controllers[0]
                val current = controller.playbackState?.position ?: 0L
                val target = kotlin.math.max(0L, current + deltaMs)
                controller.transportControls.seekTo(target)
                true
            } else false
        } catch (e: Exception) {
            false
        }
    }

    fun seekToPercent(context: Context, percent: Float): Boolean {
        return try {
            val sessionManager = context.getSystemService(Context.MEDIA_SESSION_SERVICE) as? MediaSessionManager
            val componentName = ComponentName(context, SyncNotificationListenerService::class.java)
            val controllers = sessionManager?.getActiveSessions(componentName)
            if (!controllers.isNullOrEmpty()) {
                val controller = controllers[0]
                val duration = controller.metadata?.getLong(android.media.MediaMetadata.METADATA_KEY_DURATION) ?: 0L
                val targetMs = if (duration > 0) {
                    (duration * (percent / 100f)).toLong()
                } else {
                    0L
                }
                controller.transportControls.seekTo(targetMs)
                Log.d(TAG, "Telefonda süre kaydırıldı: %$percent -> $targetMs ms")
                true
            } else false
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
                val controller = controllers[0]
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
                    com.sync.android.model.MediaInfoPayload(
                        source = "phone",
                        title = title,
                        artist = artist,
                        album = album,
                        is_playing = isPlaying,
                        position_ms = position,
                        duration_ms = duration,
                        percent = percent
                    )
                } else null
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
