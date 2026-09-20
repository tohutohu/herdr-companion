package com.tohutohu.herdrcompanion

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import com.tohutohu.herdrcompanion.data.Settings
import kotlinx.coroutines.runBlocking

/**
 * Debug builds only: configures the app without typing.
 *
 * ```
 * adb shell am broadcast -n com.tohutohu.herdrcompanion/.DebugConfigReceiver \
 *     --es gateway_url http://host:8765 --es token XXX
 * ```
 *
 * A receiver guarded by DUMP rather than extras on the exported activity, so
 * other apps on the device cannot point the app at their own gateway.
 */
class DebugConfigReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val url = intent.getStringExtra("gateway_url") ?: return
        val token = intent.getStringExtra("token") ?: return
        val container = context.container
        runBlocking { container.settings.save(Settings(url, token)) }
        container.pushRegistration.registerIfPossible()
    }
}
