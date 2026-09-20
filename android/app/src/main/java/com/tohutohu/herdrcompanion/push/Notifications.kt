package com.tohutohu.herdrcompanion.push

import android.Manifest
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.app.RemoteInput
import androidx.core.content.ContextCompat
import com.tohutohu.herdrcompanion.MainActivity
import com.tohutohu.herdrcompanion.R
import com.tohutohu.herdrcompanion.container
import com.tohutohu.herdrcompanion.data.api.Status

object Notifications {
    // A channel's importance cannot be raised after it is created, so the
    // suffix is bumped whenever the level changes and the old id is dropped.
    const val CHANNEL_COMPLETED = "completed_v2"
    const val CHANNEL_ATTENTION = "attention"
    const val CHANNEL_ERRORS = "errors"
    /** Background syncing: must never interrupt, unlike the session channels. */
    const val CHANNEL_SYNC = "sync"
    const val EXTRA_SESSION_ID = "session_id"
    const val EXTRA_STATUS = "status"
    const val EXTRA_TITLE = "title"
    const val EXTRA_BODY = "body"
    /** RemoteInput result key of the reply field. */
    const val KEY_REPLY = "reply"

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

    /**
     * Whether the notification may take the next message. A session waiting on
     * a question or an approval expects that exact answer, so it is left to the
     * in-app interaction UI instead.
     */
    fun canReply(status: String, canSend: Boolean): Boolean =
        canSend && status != Status.WAITING_INPUT && status != Status.WAITING_APPROVAL

    fun sessionIntent(context: Context, sessionId: String): PendingIntent {
        val intent = Intent(context, MainActivity::class.java)
            .setAction(Intent.ACTION_VIEW)
            .putExtra(EXTRA_SESSION_ID, sessionId)
            .putExtra("connection_id", context.container.settings.current.connectionId)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_SINGLE_TOP)
        return PendingIntent.getActivity(
            context,
            sessionId.hashCode(),
            intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
    }

    /**
     * The inline reply field. The status, title and body travel with the intent
     * so [ReplyReceiver] can re-post the same notification around the send.
     */
    private fun replyAction(context: Context, sessionId: String, status: String, title: String, body: String): NotificationCompat.Action {
        val intent = Intent(context, ReplyReceiver::class.java)
            .putExtra("connection_id", context.container.settings.current.connectionId)
            .putExtra(EXTRA_SESSION_ID, sessionId)
            .putExtra(EXTRA_STATUS, status)
            .putExtra(EXTRA_TITLE, title)
            .putExtra(EXTRA_BODY, body)
        val pending = PendingIntent.getBroadcast(
            context,
            sessionId.hashCode(),
            intent,
            // RemoteInput results are written into the intent, so it must be mutable.
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_MUTABLE,
        )
        return NotificationCompat.Action.Builder(R.drawable.ic_notification, "Reply", pending)
            .addRemoteInput(RemoteInput.Builder(KEY_REPLY).setLabel("Message").build())
            .setAllowGeneratedReplies(false)
            .build()
    }

    private fun builder(context: Context, sessionId: String, status: String, title: String, body: String) =
        NotificationCompat.Builder(context, channelFor(status))
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(title)
            .setContentText(body)
            .setStyle(NotificationCompat.BigTextStyle().bigText(body))
            .setAutoCancel(true)
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
            // Ignored from API 26 on, but it keeps the intent explicit.
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setContentIntent(sessionIntent(context, sessionId))

    fun show(context: Context, sessionId: String, status: String, title: String, body: String, canSend: Boolean) {
        val b = builder(context, sessionId, status, title, body)
        if (canReply(status, canSend)) b.addAction(replyAction(context, sessionId, status, title, body))
        post(context, sessionId, b.build())
    }

    /**
     * Re-posts the session's notification with the reply typed into it. While
     * [sending] it stays put and takes no second reply; an [error] replaces the
     * body and alerts, because the message never reached the agent.
     */
    fun showReply(
        context: Context,
        sessionId: String,
        status: String,
        title: String,
        body: String,
        reply: String,
        sending: Boolean = false,
        error: String? = null,
    ) {
        val b = builder(context, sessionId, status, title, error?.let { "Not sent: $it" } ?: body)
            .setRemoteInputHistory(arrayOf(reply))
            .setSilent(error == null)
        if (sending) {
            b.setProgress(0, 0, true).setOngoing(true).setAutoCancel(false)
        } else {
            b.addAction(replyAction(context, sessionId, status, title, body))
        }
        post(context, sessionId, b.build())
    }

    // One notification per session: a newer state replaces the older one.
    private fun post(context: Context, sessionId: String, n: Notification) {
        if (ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            return
        }
        NotificationManagerCompat.from(context).notify(sessionId.hashCode(), n)
    }

    fun cancel(context: Context, sessionId: String) {
        NotificationManagerCompat.from(context).cancel(sessionId.hashCode())
    }
}
