package com.tohutohu.herdrcompanion.push

import android.content.Context
import android.os.Build
import android.util.Log
import com.google.firebase.FirebaseApp
import com.google.firebase.messaging.FirebaseMessaging
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.tasks.await
import com.tohutohu.herdrcompanion.container
import java.util.UUID

/** Sends this device's FCM token to the gateway. */
class PushRegistration(
    private val context: Context,
    private val api: GatewayApi,
    private val scope: CoroutineScope,
) {
    private val _status = MutableStateFlow("")
    val status: StateFlow<String> = _status

    /** Firebase is only initialised when google-services.json was present at build time. */
    val isAvailable: Boolean
        get() = FirebaseApp.getApps(context).isNotEmpty()

    fun registerIfPossible() {
        if (!context.container.settings.current.isConfigured) {
            _status.value = "Pair with your Mac to configure notifications"
            return
        }
        if (!isAvailable) {
            _status.value = "Push is optional. Import Firebase settings on the Mac and pair again to enable it."
            return
        }
        scope.launch {
            try {
                register(FirebaseMessaging.getInstance().token.await())
            } catch (e: Exception) {
                Log.w(TAG, "could not get FCM token", e)
                _status.value = "Push token unavailable: ${e.message}"
            }
        }
    }

    fun register(token: String) {
        scope.launch {
            try {
                api.registerDevice(deviceName(), token)
                _status.value = "Push notifications registered"
            } catch (e: Exception) {
                Log.w(TAG, "device registration failed", e)
                _status.value = "Push registration failed: ${e.message}"
            }
        }
    }

    private fun deviceName(): String {
        val prefs = context.getSharedPreferences("push_device", Context.MODE_PRIVATE)
        val id = prefs.getString("id", null) ?: UUID.randomUUID().toString().also { prefs.edit().putString("id", it).apply() }
        return "${Build.MANUFACTURER} ${Build.MODEL} ($id)"
    }

    private companion object {
        const val TAG = "PushRegistration"
    }
}
