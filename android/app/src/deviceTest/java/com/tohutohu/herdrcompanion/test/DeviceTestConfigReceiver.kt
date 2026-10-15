package com.tohutohu.herdrcompanion.test

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import com.tohutohu.herdrcompanion.container
import com.tohutohu.herdrcompanion.data.FirebaseSettings
import com.tohutohu.herdrcompanion.data.Settings
import com.tohutohu.herdrcompanion.push.FirebaseRuntime
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json

/** adb-only setup for the isolated APK. No credentials are logged. */
class DeviceTestConfigReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        check(context.packageName.endsWith(".devicetest"))
        val url = intent.getStringExtra("gateway_url") ?: return
        val token = intent.getStringExtra("token") ?: return
        val firebase = intent.getStringExtra("firebase")?.let { Json.decodeFromString<FirebaseSettings>(it) }
        val settings = Settings(url, token, firebase,
            intent.getStringExtra("connection_id") ?: "device-audit",
            intent.getStringExtra("gateway_id").orEmpty())
        runBlocking { context.container.settings.save(settings) }
        FirebaseRuntime.initialize(context, settings)
        context.container.pushRegistration.registerIfPossible()
    }
}
