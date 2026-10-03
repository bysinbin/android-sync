package com.sync.android.service

import android.app.Notification
import android.service.notification.NotificationListenerService
import android.service.notification.StatusBarNotification
import android.util.Log

class SyncNotificationListenerService : NotificationListenerService() {

    private val TAG = "NotificationListener"

    override fun onNotificationPosted(sbn: StatusBarNotification?) {
        super.onNotificationPosted(sbn)
        if (sbn == null) return

        val pkgName = sbn.packageName
        // Kendi uygulamamızın bildirimlerini filtrele
        if (pkgName == packageName) {
            return
        }

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

        // Sistem servislerinin kalıcı (ongoing) bildirimlerini atla AMA çağrıları ASLA atlama!
        val isOngoing = (notification.flags and Notification.FLAG_ONGOING_EVENT) != 0
        if (isOngoing && !isCall) {
            return
        }

        // Eğer sistem arayüzü bildirimi ise ve arama değilse atla
        if (pkgName == "android" && !isCall) {
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

        Log.d(TAG, "Yeni Bildirim (Arama: $isCall): [$appName] $title -> $text")

        // WebSocket üzerinden Mac'e ilet
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
                text = text
            )
        }
    }

    override fun onNotificationRemoved(sbn: StatusBarNotification?) {
        super.onNotificationRemoved(sbn)
        if (sbn == null) return
        val category = sbn.notification?.category ?: ""
        val pkgName = sbn.packageName
        val isCall = category == Notification.CATEGORY_CALL ||
                     pkgName.contains("dialer") ||
                     pkgName.contains("incallui") ||
                     pkgName.contains("telecom")
        if (isCall) {
            Log.d(TAG, "Çağrı bildirimi kaldırıldı (Arama bitti veya cevaplandı)")
            val ws = SyncForegroundService.instance?.webSocketClient
            if (ws?.isConnected == true) {
                ws.sendCallState(state = "IDLE", number = "", callerName = "")
            }
        }
    }
}
