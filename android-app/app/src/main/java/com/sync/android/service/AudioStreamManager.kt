package com.sync.android.service

import android.content.Context
import android.media.AudioAttributes
import android.media.MediaPlayer
import android.util.Log

object AudioStreamManager {
    private const val TAG = "AudioStreamManager"
    private var mediaPlayer: MediaPlayer? = null
    var isPlayingMacAudio = false
        private set

    fun toggleMacAudio(context: Context, macIp: String, onStateChanged: (Boolean) -> Unit) {
        if (isPlayingMacAudio) {
            stopMacAudio()
            onStateChanged(false)
        } else {
            startMacAudio(context, macIp, onStateChanged)
        }
    }

    private fun startMacAudio(context: Context, macIp: String, onStateChanged: (Boolean) -> Unit) {
        stopMacAudio()
        try {
            val url = "http://$macIp:42424/audio/stream"
            Log.d(TAG, "Mac ses akışına bağlanılıyor: $url")
            
            mediaPlayer = MediaPlayer().apply {
                setAudioAttributes(
                    AudioAttributes.Builder()
                        .setContentType(AudioAttributes.CONTENT_TYPE_MUSIC)
                        .setUsage(AudioAttributes.USAGE_MEDIA)
                        .build()
                )
                setDataSource(url)
                setOnPreparedListener {
                    start()
                    isPlayingMacAudio = true
                    Log.d(TAG, "Mac ses akışı başladı!")
                    onStateChanged(true)
                }
                setOnErrorListener { _, what, extra ->
                    Log.e(TAG, "Mac ses akış hatası: what=$what, extra=$extra")
                    stopMacAudio()
                    onStateChanged(false)
                    true
                }
                prepareAsync()
            }
        } catch (e: Exception) {
            Log.e(TAG, "Ses başlatma hatası: ${e.message}")
            stopMacAudio()
            onStateChanged(false)
        }
    }

    fun stopMacAudio() {
        try {
            mediaPlayer?.stop()
            mediaPlayer?.release()
            mediaPlayer = null
        } catch (e: Exception) {
            Log.e(TAG, "Ses durdurma hatası: ${e.message}")
        }
        isPlayingMacAudio = false
    }
}
