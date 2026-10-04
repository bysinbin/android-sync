package com.sync.android.service

import android.annotation.SuppressLint
import android.content.Context
import android.media.*
import android.os.Process
import android.util.Base64
import android.util.Log
import com.sync.android.model.CallAudioBridgePayload
import java.util.concurrent.atomic.AtomicBoolean

class CallAudioBridgeManager(private val context: Context) {

    companion object {
        private const val TAG = "CallAudioBridge"
        const val SAMPLE_RATE = 16000
        const val CHANNEL_IN = AudioFormat.CHANNEL_IN_MONO
        const val CHANNEL_OUT = AudioFormat.CHANNEL_OUT_MONO
        const val AUDIO_FORMAT = AudioFormat.ENCODING_PCM_16BIT
        var instance: CallAudioBridgeManager? = null
    }

    init {
        instance = this
    }

    private val audioManager = context.getSystemService(Context.AUDIO_SERVICE) as? AudioManager
    private var audioRecord: AudioRecord? = null
    private var audioTrack: AudioTrack? = null
    private val isStreaming = AtomicBoolean(false)
    private var recordThread: Thread? = null

    var onAudioDataCaptured: ((String) -> Unit)? = null

    @SuppressLint("MissingPermission")
    fun startBridge() {
        if (isStreaming.get()) return
        try {
            audioManager?.mode = AudioManager.MODE_IN_COMMUNICATION
            audioManager?.isSpeakerphoneOn = true

            // 1. Setup AudioTrack for playing incoming PC voice
            val minPlayBuf = AudioTrack.getMinBufferSize(SAMPLE_RATE, CHANNEL_OUT, AUDIO_FORMAT)
            audioTrack = AudioTrack.Builder()
                .setAudioAttributes(
                    AudioAttributes.Builder()
                        .setUsage(AudioAttributes.USAGE_VOICE_COMMUNICATION)
                        .setContentType(AudioAttributes.CONTENT_TYPE_SPEECH)
                        .build()
                )
                .setAudioFormat(
                    AudioFormat.Builder()
                        .setEncoding(AUDIO_FORMAT)
                        .setSampleRate(SAMPLE_RATE)
                        .setChannelMask(CHANNEL_OUT)
                        .build()
                )
                .setBufferSizeInBytes(minPlayBuf.coerceAtLeast(4096))
                .setTransferMode(AudioTrack.MODE_STREAM)
                .build()
            audioTrack?.play()

            // 2. Setup AudioRecord for capturing phone mic to send to PC
            val minRecBuf = AudioRecord.getMinBufferSize(SAMPLE_RATE, CHANNEL_IN, AUDIO_FORMAT)
            val recBufSize = (minRecBuf * 2).coerceAtLeast(2048)
            audioRecord = AudioRecord(
                MediaRecorder.AudioSource.VOICE_COMMUNICATION,
                SAMPLE_RATE,
                CHANNEL_IN,
                AUDIO_FORMAT,
                recBufSize
            )

            if (audioRecord?.state != AudioRecord.STATE_INITIALIZED) {
                // Fallback to standard MIC
                audioRecord?.release()
                audioRecord = AudioRecord(
                    MediaRecorder.AudioSource.MIC,
                    SAMPLE_RATE,
                    CHANNEL_IN,
                    AUDIO_FORMAT,
                    recBufSize
                )
            }

            audioRecord?.startRecording()
            isStreaming.set(true)

            recordThread = Thread {
                Process.setThreadPriority(Process.THREAD_PRIORITY_URGENT_AUDIO)
                val buffer = ByteArray(1024)
                while (isStreaming.get()) {
                    val read = audioRecord?.read(buffer, 0, buffer.size) ?: -1
                    if (read > 0) {
                        val b64 = Base64.encodeToString(buffer, 0, read, Base64.NO_WRAP)
                        onAudioDataCaptured?.invoke(b64)
                    }
                }
            }.apply { start() }

            Log.d(TAG, "Sesli görüşme köprüsü (Hands-Free) başlatıldı: 16kHz PCM mono")
        } catch (e: Exception) {
            Log.e(TAG, "AudioBridge başlatma hatası: ${e.message}", e)
            stopBridge()
        }
    }

    fun handleIncomingPCAudio(base64Data: String) {
        if (!isStreaming.get() || audioTrack == null) return
        try {
            val pcmBytes = Base64.decode(base64Data, Base64.NO_WRAP)
            audioTrack?.write(pcmBytes, 0, pcmBytes.size)
        } catch (e: Exception) {
            // Drop damaged packets
        }
    }

    fun stopBridge() {
        if (!isStreaming.getAndSet(false)) return
        try {
            recordThread?.interrupt()
            recordThread = null

            audioRecord?.stop()
            audioRecord?.release()
            audioRecord = null

            audioTrack?.stop()
            audioTrack?.release()
            audioTrack = null

            audioManager?.mode = AudioManager.MODE_NORMAL
            Log.d(TAG, "Sesli görüşme köprüsü durduruldu.")
        } catch (e: Exception) {
            Log.e(TAG, "AudioBridge durdurma hatası: ${e.message}")
        }
    }

    fun isBridgeActive(): Boolean = isStreaming.get()
}
