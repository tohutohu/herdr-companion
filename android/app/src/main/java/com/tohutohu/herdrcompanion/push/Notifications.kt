package com.tohutohu.herdrcompanion.push

import android.Manifest
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationChannelGroup
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
    const val CHANNEL_RESIDENT = "resident"
    const val CHANNEL_RESIDENT_ATTENTION = "resident_attention"
    const val CHANNEL_RESIDENT_ERRORS = "resident_errors"
    const val EXTRA_RESIDENT_TASK = "resident_task_id"
    private const val TAG_RESIDENT = "resident"
    private const val TAG_SUMMARY = "summary"
    private const val SEEN_PREFS = "resident_notifications"
    private const val SEEN_KEEP = 64
    const val EXTRA_SESSION_ID = "session_id"
    const val EXTRA_STATUS = "status"
    const val EXTRA_TITLE = "title"
    const val EXTRA_BODY = "body"
    /** RemoteInput result key of the reply field. */
    const val KEY_REPLY = "reply"

    private val RETIRED_CHANNELS = listOf("completed")

    /** A channel group in the system settings, and a bundle of its own in the shade. */
    private enum class Section(val key: String, val label: String, val summaryId: Int) {
        SESSIONS("sessions", "Sessions", 1),
        RESIDENT("resident", "常駐", 2),
    }

    fun createChannels(context: Context) {
        val nm = context.getSystemService(NotificationManager::class.java)
        nm.createNotificationChannelGroups(Section.entries.map { NotificationChannelGroup(it.key, it.label) })
        fun channel(id: String, name: String, section: Section) =
            NotificationChannel(id, name, NotificationManager.IMPORTANCE_HIGH).apply { group = section.key }
        nm.createNotificationChannels(
            listOf(
                // All of these pop up as a heads-up: an agent that stopped is
                // worth looking at right away.
                channel(CHANNEL_COMPLETED, "Completed", Section.SESSIONS),
                channel(CHANNEL_ATTENTION, "Needs attention", Section.SESSIONS),
                channel(CHANNEL_ERRORS, "Errors", Section.SESSIONS),
                channel(CHANNEL_RESIDENT, "報告", Section.RESIDENT),
                channel(CHANNEL_RESIDENT_ATTENTION, "判断待ち", Section.RESIDENT),
                channel(CHANNEL_RESIDENT_ERRORS, "失敗", Section.RESIDENT),
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

    private fun sectioned(context: Context, channel: String, section: Section) =
        NotificationCompat.Builder(context, channel)
            .setSmallIcon(R.drawable.ic_notification)
            .setGroup(section.key)
            .setSubText(section.label)

    /** Inside a bundle only the title and body are left to tell a failure apart. */
    internal fun displayTitle(channel: String, title: String): String =
        if (channel == CHANNEL_ERRORS || channel == CHANNEL_RESIDENT_ERRORS) "⚠ $title" else title

    private fun builder(context: Context, channel: String, sessionId: String, title: String, body: String) =
        sectioned(context, channel, Section.SESSIONS)
            .setContentTitle(displayTitle(channel, title))
            .setContentText(body)
            .setStyle(NotificationCompat.BigTextStyle().bigText(body))
            .setAutoCancel(true)
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
            // Ignored from API 26 on, but it keeps the intent explicit.
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setContentIntent(sessionIntent(context, sessionId))

    fun show(context: Context, sessionId: String, status: String, title: String, body: String, canSend: Boolean) {
        val channel = channelFor(status)
        val b = builder(context, channel, sessionId, title, body)
        if (canReply(status, canSend)) b.addAction(replyAction(context, sessionId, status, title, body))
        post(context, null, sessionId.hashCode(), channel, Section.SESSIONS, b.build())
    }

    /**
     * Re-posts the session's notification with the reply typed into it. While
     * [sending] it stays put and takes no second reply; an [error] replaces the
     * body and alerts on the error channel, whatever the session's own state is.
     * An uncertain outcome asks the user to check the conversation.
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
        uncertain: Boolean = false,
    ) {
        val channel = if (error != null) CHANNEL_ERRORS else channelFor(status)
        val b = builder(context, channel, sessionId, title, error?.let { if (uncertain) "Delivery unconfirmed: $it" else "Not sent: $it" } ?: body)
            .setRemoteInputHistory(arrayOf(replyHistoryPreview(reply)))
            .setSilent(error == null)
        if (sending) {
            b.setProgress(0, 0, true).setOngoing(true).setAutoCancel(false)
        } else if (!uncertain) {
            b.addAction(replyAction(context, sessionId, status, title, body))
        }
        post(context, null, sessionId.hashCode(), channel, Section.SESSIONS, b.build())
    }

    // One notification per session or task: a newer state replaces the older
    // one. The summary only holds the bundle together, so it never alerts; it
    // follows the newest channel so that silencing another one cannot hide it.
    private fun post(context: Context, tag: String?, id: Int, channel: String, section: Section, n: Notification) {
        if (ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            return
        }
        val summary = sectioned(context, channel, section)
            .setContentTitle(section.label)
            .setGroupSummary(true)
            .setGroupAlertBehavior(NotificationCompat.GROUP_ALERT_CHILDREN)
            .setAutoCancel(true)
            .build()
        val manager = NotificationManagerCompat.from(context)
        manager.notify(tag, id, n)
        manager.notify(TAG_SUMMARY, section.summaryId, summary)
    }

    /** Cancelling from code leaves the summary behind, so the last one takes it along. */
    private fun cancel(context: Context, tag: String?, id: Int, section: Section) {
        val manager = NotificationManagerCompat.from(context)
        manager.cancel(tag, id)
        // The cancel above may not have been applied yet, so it is excluded by hand.
        val othersRemain = context.getSystemService(NotificationManager::class.java).activeNotifications.any {
            it.notification.group == section.key &&
                it.notification.flags and Notification.FLAG_GROUP_SUMMARY == 0 &&
                !(it.tag == tag && it.id == id)
        }
        if (!othersRemain) manager.cancel(TAG_SUMMARY, section.summaryId)
    }

    fun cancel(context: Context, sessionId: String) = cancel(context, null, sessionId.hashCode(), Section.SESSIONS)

    fun residentChannel(kind: String): String = when (kind) {
        "question" -> CHANNEL_RESIDENT_ATTENTION
        "failure" -> CHANNEL_RESIDENT_ERRORS
        else -> CHANNEL_RESIDENT
    }

    fun residentIntent(context: Context, taskId: String): PendingIntent {
        val intent = Intent(context, MainActivity::class.java)
            .setAction(Intent.ACTION_VIEW)
            .putExtra(EXTRA_RESIDENT_TASK, taskId)
            .putExtra("connection_id", context.container.settings.current.connectionId)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_SINGLE_TOP)
        return PendingIntent.getActivity(
            context,
            ("resident:$taskId").hashCode(),
            intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
    }

    /**
     * Shows a resident task's report. One notification per task (a newer
     * one replaces it); an entry the gateway delivers again with the same
     * [notificationId] is not shown a second time. The task's conversation in
     * the app is the record, not the notification.
     */
    fun showResident(context: Context, taskId: String, notificationId: String, kind: String, title: String, body: String): Boolean {
        if (!markShown(context, notificationId)) return false
        val channel = residentChannel(kind)
        val n = sectioned(context, channel, Section.RESIDENT)
            .setContentTitle(displayTitle(channel, title))
            .setContentText(body)
            .setStyle(NotificationCompat.BigTextStyle().bigText(body))
            .setAutoCancel(true)
            .setCategory(if (kind == "question") NotificationCompat.CATEGORY_REMINDER else NotificationCompat.CATEGORY_STATUS)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setContentIntent(residentIntent(context, taskId))
            .build()
        post(context, TAG_RESIDENT, taskId.hashCode(), channel, Section.RESIDENT, n)
        return true
    }

    fun cancelResident(context: Context, taskId: String) = cancel(context, TAG_RESIDENT, taskId.hashCode(), Section.RESIDENT)

    /** Records [notificationId] as shown; false when it already was. */
    @Synchronized
    internal fun markShown(context: Context, notificationId: String): Boolean {
        val prefs = context.getSharedPreferences(SEEN_PREFS, Context.MODE_PRIVATE)
        val seen = prefs.getString("ids", "").orEmpty().split('\n').filter { it.isNotEmpty() }
        if (notificationId in seen) return false
        prefs.edit().putString("ids", (seen + notificationId).takeLast(SEEN_KEEP).joinToString("\n")).apply()
        return true
    }
}

/** Keep notification Binder payloads small; the complete reply remains in private storage. */
internal fun replyHistoryPreview(reply: String): String {
    if (reply.length <= 2_000) return reply
    val end = if (reply[1_999].isHighSurrogate()) 1_999 else 2_000
    return reply.substring(0, end) + "…"
}
