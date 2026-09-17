package com.tohutohu.herdrmobile.push

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import com.tohutohu.herdrmobile.MainActivity
import com.tohutohu.herdrmobile.R
import com.tohutohu.herdrmobile.data.api.Status

object Notifications {
    // A channel's importance cannot be raised after it is created, so the
    // suffix is bumped whenever the level changes and the old id is dropped.
    const val CHANNEL_COMPLETED = "completed_v2"
    const val CHANNEL_ATTENTION = "attention"
    const val CHANNEL_ERRORS = "errors"
    /** Background syncing: must never interrupt, unlike the session channels. */
    const val CHANNEL_SYNC = "sync"
    const val EXTRA_SESSION_ID = "session_id"

    private val RETIRED_CHANNELS = listOf("completed")

    fun createChannels(context: Context) {
        val nm = context.getSystemService(NotificationManager::class.java)
        nm.createNotificationChannels(
            listOf(
                // All three pop up as a heads-up: an agent that stopped is
                // worth looking at right away.
                NotificationChannel(CHANNEL_COMPLETED, "Completed", NotificationManager.IMPORTANCE_HIGH),
                NotificationChannel(CHANNEL_ATTENTION, "Needs attention", NotificationManager.IMPORTANCE_HIGH),
                NotificationChannel(CHANNEL_ERRORS, "Errors", NotificationManager.IMPORTANCE_HIGH),
                NotificationChannel(CHANNEL_SYNC, "Syncing", NotificationManager.IMPORTANCE_LOW),
            ),
        )
        RETIRED_CHANNELS.forEach { nm.deleteNotificationChannel(it) }
    }

    fun channelFor(status: String): String = when (status) {
        Status.WAITING_INPUT, Status.WAITING_APPROVAL -> CHANNEL_ATTENTION
        Status.FAILED -> CHANNEL_ERRORS
        else -> CHANNEL_COMPLETED
    }

    fun sessionIntent(context: Context, sessionId: String): PendingIntent {
        val intent = Intent(context, MainActivity::class.java)
            .setAction(Intent.ACTION_VIEW)
            .putExtra(EXTRA_SESSION_ID, sessionId)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_SINGLE_TOP)
        return PendingIntent.getActivity(
            context,
            sessionId.hashCode(),
            intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
    }

    fun show(context: Context, sessionId: String, status: String, title: String, body: String) {
        if (ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            return
        }
        val n = NotificationCompat.Builder(context, channelFor(status))
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(title)
            .setContentText(body)
            .setStyle(NotificationCompat.BigTextStyle().bigText(body))
            .setAutoCancel(true)
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
            // Ignored from API 26 on, but it keeps the intent explicit.
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setContentIntent(sessionIntent(context, sessionId))
            .build()
        // One notification per session: a newer state replaces the older one.
        NotificationManagerCompat.from(context).notify(sessionId.hashCode(), n)
    }

    fun cancel(context: Context, sessionId: String) {
        NotificationManagerCompat.from(context).cancel(sessionId.hashCode())
    }
}
