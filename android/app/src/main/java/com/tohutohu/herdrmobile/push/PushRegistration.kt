package com.tohutohu.herdrmobile.push

import android.content.Context
import android.os.Build
import android.util.Log
import com.google.firebase.FirebaseApp
import com.google.firebase.messaging.FirebaseMessaging
import com.tohutohu.herdrmobile.data.api.GatewayApi
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.tasks.await

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
        if (!isAvailable) {
            _status.value = "Push disabled (no google-services.json in this build)"
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

    private fun deviceName(): String = "${Build.MANUFACTURER} ${Build.MODEL}".trim()

    private companion object {
        const val TAG = "PushRegistration"
    }
}
