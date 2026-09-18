package com.tohutohu.herdrmobile.push

import android.content.Context
import com.google.firebase.FirebaseApp
import com.google.firebase.FirebaseOptions
import com.tohutohu.herdrmobile.data.Settings

object FirebaseRuntime {
    /** Called before push registration, including when a push starts a new process. */
    fun initialize(context: Context, settings: Settings) {
        if (FirebaseApp.getApps(context).any { it.name == FirebaseApp.DEFAULT_APP_NAME }) return
        val config = settings.firebase
        if (config != null) {
            FirebaseApp.initializeApp(context, FirebaseOptions.Builder()
                .setApiKey(config.apiKey).setApplicationId(config.applicationId)
                .setProjectId(config.projectId).setGcmSenderId(config.senderId).build())
        } else if (settings.connectionId.isEmpty()) {
            // Compatibility for explicitly bundled personal builds.
            FirebaseApp.initializeApp(context)
        }
    }
}
