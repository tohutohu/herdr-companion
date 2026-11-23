package com.tohutohu.herdrcompanion

import android.content.Intent
import com.tohutohu.herdrcompanion.data.SessionLink
import com.tohutohu.herdrcompanion.push.Notifications

/** Where an intent that reaches [MainActivity] asks to go. */
internal sealed interface LaunchTarget {
    /** An `hc://session/<id>` link. Any app or web page can send one. */
    data class Link(val sessionId: String) : LaunchTarget
    data class SessionNotification(val sessionId: String) : LaunchTarget
    data class ResidentNotification(val taskId: String) : LaunchTarget

    companion object {
        /**
         * Notification taps are ACTION_VIEW too, but carry extras instead of a
         * URI, so only an intent with data is read as a link. Notification
         * extras count only for the current connection: a notification posted
         * before re-pairing names a session of another Gateway.
         */
        fun of(intent: Intent?, connectionId: String, configured: Boolean): LaunchTarget? {
            intent ?: return null
            if (intent.action == Intent.ACTION_VIEW && intent.data != null) {
                val id = intent.dataString?.let(SessionLink::sessionId) ?: return null
                return if (configured) Link(id) else null
            }
            if (intent.getStringExtra(Notifications.EXTRA_CONNECTION_ID).orEmpty() != connectionId) return null
            intent.getStringExtra(Notifications.EXTRA_RESIDENT_TASK)?.let { return ResidentNotification(it) }
            return intent.getStringExtra(Notifications.EXTRA_SESSION_ID)?.let(::SessionNotification)
        }
    }
}
