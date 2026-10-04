package com.sync.android.service

import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.util.Log
import com.sync.android.model.InstalledAppInfo

class AppLaunchManager(private val context: Context) {

    companion object {
        private const val TAG = "AppLaunchManager"
        var instance: AppLaunchManager? = null
    }

    init {
        instance = this
    }

    fun getInstalledApps(): List<InstalledAppInfo> {
        val pm = context.packageManager
        val intent = Intent(Intent.ACTION_MAIN, null).apply {
            addCategory(Intent.CATEGORY_LAUNCHER)
        }

        val apps = mutableListOf<InstalledAppInfo>()
        try {
            val resolveInfos = pm.queryIntentActivities(intent, 0)
            for (info in resolveInfos) {
                val pkg = info.activityInfo.packageName
                if (pkg == context.packageName) continue // Kendisini filtrele
                val label = info.loadLabel(pm).toString().trim()
                if (label.isNotEmpty() && pkg.isNotEmpty()) {
                    apps.add(InstalledAppInfo(name = label, package_name = pkg))
                }
            }
            apps.sortBy { it.name.lowercase() }
        } catch (e: Exception) {
            Log.e(TAG, "Uygulama listesi alınamadı: ${e.message}")
        }
        return apps
    }

    fun launchApp(packageName: String): Boolean {
        return try {
            val pm = context.packageManager
            val intent = pm.getLaunchIntentForPackage(packageName)
            if (intent != null) {
                intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_RESET_TASK_IF_NEEDED)
                context.startActivity(intent)
                Log.d(TAG, "Uygulama başlatıldı: $packageName")
                true
            } else {
                Log.w(TAG, "Uygulama bulunamadı veya başlatılamıyor: $packageName")
                false
            }
        } catch (e: Exception) {
            Log.e(TAG, "Uygulama başlatma hatası ($packageName): ${e.message}")
            false
        }
    }
}
