package com.sync.android.service

import android.app.Notification
import android.app.RemoteInput
import android.content.Intent
import android.os.Bundle
import android.service.notification.NotificationListenerService
import android.service.notification.StatusBarNotification
import android.util.Log
import java.util.concurrent.ConcurrentHashMap

class SyncNotificationListenerService : NotificationListenerService() {

    companion object {
        private const val TAG = "NotificationListener"
        var instance: SyncNotificationListenerService? = null
        val cachedNotifications = ConcurrentHashMap<String, StatusBarNotification>()
        val recentNotifications = java.util.concurrent.CopyOnWriteArrayList<Map<String, Any>>()
        var currentCallState: Map<String, String> = mapOf("state" to "IDLE", "phone_number" to "", "caller_name" to "")

        fun replyToNotification(key: String, actionIndex: Int, text: String): Boolean {
            val service = instance ?: return false
            val sbn = cachedNotifications[key] ?: return false
            val actions = sbn.notification?.actions ?: return false

            var targetAction: Notification.Action? = null
            var targetRemoteInput: RemoteInput? = null

            if (actionIndex in actions.indices) {
                val a = actions[actionIndex]
                val ri = a.remoteInputs?.firstOrNull()
                if (ri != null) {
                    targetAction = a
                    targetRemoteInput = ri
                }
            }

            if (targetAction == null) {
                // Find first action that has RemoteInput
                for (a in actions) {
                    val ri = a.remoteInputs?.firstOrNull()
                    if (ri != null) {
                        targetAction = a
                        targetRemoteInput = ri
                        break
                    }
                }
            }

            if (targetAction == null || targetRemoteInput == null) {
                Log.w(TAG, "Cevaplanabilir aksiyon bulunamadı: $key")
                return false
            }

            try {
                val intent = Intent()
                val bundle = Bundle()
                bundle.putCharSequence(targetRemoteInput.resultKey, text)
                RemoteInput.addResultsToIntent(targetAction.remoteInputs, intent, bundle)
                targetAction.actionIntent.send(service, 0, intent)
                Log.d(TAG, "Bildirime başarıyla cevap gönderildi ($key): $text")
                return true
            } catch (e: Exception) {
                Log.e(TAG, "Cevap gönderme hatası ($key): ${e.message}", e)
                return false
            }
        }

        fun executeNotificationAction(key: String, actionIndex: Int): Boolean {
            val service = instance ?: return false
            val sbn = cachedNotifications[key] ?: return false
            val actions = sbn.notification?.actions ?: return false
            if (actionIndex in actions.indices) {
                val a = actions[actionIndex]
                try {
                    a.actionIntent.send()
                    Log.d(TAG, "Bildirim eylemi tetiklendi ($key): #${actionIndex} ${a.title}")
                    return true
                } catch (e: Exception) {
                    Log.e(TAG, "Bildirim eylemi tetikleme hatası ($key): ${e.message}", e)
                    return false
                }
            }
            Log.w(TAG, "Aksiyon indeksi bulunamadı ($key, idx: $actionIndex)")
            return false
        }

        fun dismissNotification(key: String): Boolean {
            val service = instance ?: return false
            return try {
                service.cancelNotification(key)
                cachedNotifications.remove(key)
                Log.d(TAG, "Bildirim başarıyla kapatıldı ($key)")
                true
            } catch (e: Exception) {
                Log.e(TAG, "Bildirim kapatma hatası ($key): ${e.message}", e)
                false
            }
        }
    }

    override fun onCreate() {
        super.onCreate()
        instance = this
    }

    override fun onDestroy() {
        super.onDestroy()
        if (instance == this) instance = null
        cachedNotifications.clear()
    }

    override fun onNotificationPosted(sbn: StatusBarNotification?) {
        super.onNotificationPosted(sbn)
        if (sbn == null) return

        val pkgName = sbn.packageName
        val isPCNotif = sbn.notification?.channelId == SyncForegroundService.CHANNEL_PC_NOTIF_ID
        if (pkgName == packageName && !isPCNotif) {
            return
        }

        cachedNotifications[sbn.key] = sbn

        val notification = sbn.notification ?: return
        val extras = notification.extras ?: return
        val title = extras.getCharSequence(Notification.EXTRA_TITLE)?.toString() ?: ""
        val text = extras.getCharSequence(Notification.EXTRA_TEXT)?.toString() 
            ?: extras.getCharSequence(Notification.EXTRA_BIG_TEXT)?.toString() 
            ?: ""

        val category = notification.category ?: ""
        val isCall = category == Notification.CATEGORY_CALL ||
                     category == Notification.CATEGORY_MISSED_CALL ||
                     pkgName.contains("dialer") ||
                     pkgName.contains("incallui") ||
                     pkgName.contains("telecom") ||
                     pkgName.contains("phone") ||
                     extras.containsKey(Notification.EXTRA_CALL_PERSON) ||
                     notification.actions?.any { action ->
                         val aTitle = action.title?.toString()?.lowercase() ?: ""
                         aTitle.contains("cevapla") || aTitle.contains("answer") || aTitle.contains("reddet") || aTitle.contains("decline")
                     } == true

        val isOngoing = (notification.flags and Notification.FLAG_ONGOING_EVENT) != 0
        if (isOngoing && !isCall) {
            return
        }

        if (pkgName == "android" && !isCall && notification.channelId != "shell_cmd") {
            return
        }

        if (title.isEmpty() && text.isEmpty()) return

        val appName = try {
            val pm = packageManager
            val appInfo = pm.getApplicationInfo(pkgName, 0)
            pm.getApplicationLabel(appInfo).toString()
        } catch (e: Exception) {
            pkgName
        }

        // Check if notification can be replied inline and extract all action buttons
        val actionList = mutableListOf<com.sync.android.model.NotificationActionItem>()
        notification.actions?.forEachIndexed { idx, act ->
            val aTitle = act.title?.toString()?.trim() ?: ""
            if (aTitle.isNotEmpty()) {
                val isReply = act.remoteInputs != null && act.remoteInputs.isNotEmpty()
                actionList.add(
                    com.sync.android.model.NotificationActionItem(
                        index = idx,
                        title = aTitle,
                        is_reply = isReply
                    )
                )
            }
        }
        val canReply = actionList.any { it.is_reply }

        Log.d(TAG, "Yeni Bildirim (Arama: $isCall, Yanıtlanabilir: $canReply, Aksiyonlar: ${actionList.size}): [$appName] $title -> $text")

        val notifId = "${sbn.id}_${sbn.postTime}"
        val notifMap = mapOf(
            "id" to notifId,
            "package_name" to pkgName,
            "app_name" to (if (isCall) "📞 $appName (Gelen Arama)" else appName),
            "title" to title,
            "text" to text,
            "timestamp" to sbn.postTime
        )
        recentNotifications.removeIf { it["id"] == notifId || (it["title"] == title && it["text"] == text) }
        recentNotifications.add(0, notifMap)
        while (recentNotifications.size > 25) {
            recentNotifications.removeAt(recentNotifications.size - 1)
        }

        if (isCall) {
            val caller = if (title.isNotEmpty()) title else text
            val number = if (text.isNotEmpty() && text != title) text else ""
            currentCallState = mapOf("state" to "RINGING", "phone_number" to number, "caller_name" to caller)
        }

        val ws = SyncForegroundService.instance?.webSocketClient
        if (ws?.isConnected == true) {
            if (isCall) {
                val caller = if (title.isNotEmpty()) title else text
                val number = if (text.isNotEmpty() && text != title) text else ""
                ws.sendCallState(state = "RINGING", number = number, callerName = caller)
            }

            ws.sendNotification(
                id = "${sbn.id}_${sbn.postTime}",
                pkg = pkgName,
                appName = if (isCall) "📞 $appName (Gelen Arama)" else appName,
                title = title,
                text = text,
                key = sbn.key,
                canReply = canReply,
                actions = if (actionList.isNotEmpty()) actionList else null
            )
        }
    }

    override fun onNotificationRemoved(sbn: StatusBarNotification?) {
        super.onNotificationRemoved(sbn)
        if (sbn == null) return
        cachedNotifications.remove(sbn.key)

        val category = sbn.notification?.category ?: ""
        val pkgName = sbn.packageName
        val isCall = category == Notification.CATEGORY_CALL ||
                     pkgName.contains("dialer") ||
                     pkgName.contains("incallui") ||
                     pkgName.contains("telecom")
        val ws = SyncForegroundService.instance?.webSocketClient
        if (ws?.isConnected == true) {
            if (isCall) {
                Log.d(TAG, "Çağrı bildirimi kaldırıldı (Arama bitti veya cevaplandı)")
                currentCallState = mapOf("state" to "IDLE", "phone_number" to "", "caller_name" to "")
                ws.sendCallState(state = "IDLE", number = "", callerName = "")
            }
            val notifId = "${sbn.id}_${sbn.postTime}"
            recentNotifications.removeIf { it["id"] == notifId }
            // Kendi servis bildirimimiz haricindekileri PC'ye kapatıldı olarak ilet
            if (pkgName != packageName) {
                ws.sendNotificationDismiss(sbn.key, sbn.id.toString())
            }
        }
    }
}

