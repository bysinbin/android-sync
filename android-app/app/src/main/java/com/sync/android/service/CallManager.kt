package com.sync.android.service

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.media.AudioManager
import android.net.Uri
import android.os.Build
import android.telecom.TelecomManager
import android.util.Log
import androidx.core.content.ContextCompat

object CallManager {
    private const val TAG = "CallManager"

    fun handleCallAction(context: Context, action: String, number: String? = null, value: Boolean? = null) {
        val telecom = context.getSystemService(Context.TELECOM_SERVICE) as? TelecomManager
        val audioManager = context.getSystemService(Context.AUDIO_SERVICE) as? AudioManager

        Log.d(TAG, "Çağrı aksiyonu yürütülüyor: $action (numara=$number, deger=$value)")

        when (action.uppercase()) {
            "ANSWER", "ACCEPT" -> {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                    if (ContextCompat.checkSelfPermission(context, Manifest.permission.ANSWER_PHONE_CALLS) == PackageManager.PERMISSION_GRANTED) {
                        try {
                            telecom?.acceptRingingCall()
                            Log.d(TAG, "Çağrı başarıyla yanıtlandı!")
                            // Ses modunu ayarla
                            audioManager?.mode = AudioManager.MODE_IN_COMMUNICATION
                            audioManager?.isSpeakerphoneOn = true
                        } catch (e: Exception) {
                            Log.e(TAG, "acceptRingingCall hatası: ${e.message}")
                        }
                    } else {
                        Log.w(TAG, "ANSWER_PHONE_CALLS izni verilmemiş!")
                    }
                }
            }

            "REJECT", "DECLINE", "HANGUP", "END" -> {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
                    if (ContextCompat.checkSelfPermission(context, Manifest.permission.ANSWER_PHONE_CALLS) == PackageManager.PERMISSION_GRANTED) {
                        try {
                            telecom?.endCall()
                            Log.d(TAG, "Çağrı başarıyla sonlandırıldı!")
                        } catch (e: Exception) {
                            Log.e(TAG, "endCall hatası: ${e.message}")
                        }
                    } else {
                        Log.w(TAG, "ANSWER_PHONE_CALLS izni verilmemiş!")
                    }
                }
            }

            "DIAL", "CALL" -> {
                if (!number.isNullOrEmpty()) {
                    try {
                        val hasCallPerm = ContextCompat.checkSelfPermission(context, Manifest.permission.CALL_PHONE) == PackageManager.PERMISSION_GRANTED
                        val intentAction = if (hasCallPerm) Intent.ACTION_CALL else Intent.ACTION_DIAL
                        val intent = Intent(intentAction, Uri.parse("tel:$number")).apply {
                            flags = Intent.FLAG_ACTIVITY_NEW_TASK
                        }
                        context.startActivity(intent)
                        Log.d(TAG, "Arama başlatıldı: $number")
                    } catch (e: Exception) {
                        Log.e(TAG, "Arama başlatma hatası: ${e.message}")
                    }
                }
            }

            "SET_SPEAKER" -> {
                val enable = value ?: true
                audioManager?.mode = AudioManager.MODE_IN_COMMUNICATION
                audioManager?.isSpeakerphoneOn = enable
                Log.d(TAG, "Hoparlör durumu: $enable")
            }

            "SET_MUTE" -> {
                val enable = value ?: true
                audioManager?.isMicrophoneMute = enable
                Log.d(TAG, "Mikrofon sessiz: $enable")
            }
        }
    }
}
