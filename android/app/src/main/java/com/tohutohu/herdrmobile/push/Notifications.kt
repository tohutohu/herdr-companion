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
    const val CHANNEL_COMPLETED = "completed"
    const val CHANNEL_ATTENTION = "attention"
    const val CHANNEL_ERRORS = "errors"
    const val EXTRA_SESSION_ID = "session_id"

    fun createChannels(context: Context) {
        val nm = context.getSystemService(NotificationManager::class.java)
        nm.createNotificationChannels(
            listOf(
                NotificationChannel(CHANNEL_COMPLETED, "Completed", NotificationManager.IMPORTANCE_DEFAULT),
                NotificationChannel(CHANNEL_ATTENTION, "Needs attention", NotificationManager.IMPORTANCE_HIGH),
                NotificationChannel(CHANNEL_ERRORS, "Errors", NotificationManager.IMPORTANCE_HIGH),
            ),
        )
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
            .setContentIntent(sessionIntent(context, sessionId))
            .build()
        // One notification per session: a newer state replaces the older one.
        NotificationManagerCompat.from(context).notify(sessionId.hashCode(), n)
    }

    fun cancel(context: Context, sessionId: String) {
        NotificationManagerCompat.from(context).cancel(sessionId.hashCode())
    }
}
