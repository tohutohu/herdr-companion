package com.tohutohu.herdrcompanion.push

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log
import androidx.core.app.NotificationCompat
import androidx.core.app.RemoteInput
import androidx.work.BackoffPolicy
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingWorkPolicy
import androidx.work.ForegroundInfo
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.OutOfQuotaPolicy
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import com.tohutohu.herdrcompanion.R
import com.tohutohu.herdrcompanion.container
import com.tohutohu.herdrcompanion.data.api.GatewayException
import java.util.concurrent.TimeUnit

/** Takes the text typed into a notification's reply field and hands it to [ReplyWorker]. */
class ReplyReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.getStringExtra("connection_id").orEmpty() != context.container.settings.current.connectionId) return
        val sessionId = intent.getStringExtra(Notifications.EXTRA_SESSION_ID) ?: return
        val reply = RemoteInput.getResultsFromIntent(intent)
            ?.getCharSequence(Notifications.KEY_REPLY)?.toString()?.trim()
        if (reply.isNullOrEmpty()) return
        val status = intent.getStringExtra(Notifications.EXTRA_STATUS).orEmpty()
        val title = intent.getStringExtra(Notifications.EXTRA_TITLE).orEmpty()
        val body = intent.getStringExtra(Notifications.EXTRA_BODY).orEmpty()
        // The shade collapses right away, so the notification itself has to
        // report what happens to the message from here on.
        Notifications.showReply(context, sessionId, status, title, body, reply, sending = true)
        ReplyWorker.enqueue(context, sessionId, status, title, body, reply)
    }
}

/**
 * Sends one notification reply. It runs as work rather than inside the
 * receiver so a gateway that is momentarily unreachable gets another try.
 */
class ReplyWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    override suspend fun doWork(): Result {
        if (inputData.getString("connection_id").orEmpty() != applicationContext.container.settings.current.connectionId) return Result.failure()
        val sessionId = inputData.getString(KEY_SESSION_ID) ?: return Result.failure()
        val reply = inputData.getString(KEY_REPLY) ?: return Result.failure()
        val status = inputData.getString(KEY_STATUS).orEmpty()
        val title = inputData.getString(KEY_TITLE).orEmpty()
        val body = inputData.getString(KEY_BODY).orEmpty()
        val repo = applicationContext.container.repository
        return try {
            repo.send(sessionId, reply, emptyList())
            // The agent is working again; show that without waiting for a push.
            runCatching { repo.refreshSessions() }
            Notifications.showReply(applicationContext, sessionId, status, title, body, reply)
            Result.success()
        } catch (e: GatewayException) {
            Log.w(TAG, "reply failed for $sessionId: ${e.code} ${e.message}")
            // Client errors (unknown session, bad token) will not fix themselves.
            if (e.code in 400..499 || runAttemptCount >= 3) {
                giveUp(sessionId, status, title, body, reply, e.message ?: "HTTP ${e.code}")
            } else {
                Result.retry()
            }
        } catch (e: Exception) {
            Log.w(TAG, "reply failed for $sessionId", e)
            if (runAttemptCount < 3) Result.retry() else giveUp(sessionId, status, title, body, reply, e.message ?: e.toString())
        }
    }

    private fun giveUp(sessionId: String, status: String, title: String, body: String, reply: String, error: String): Result {
        Notifications.showReply(applicationContext, sessionId, status, title, body, reply, error = error)
        return Result.failure()
    }

    // Required for expedited work on Android 11 and lower.
    override suspend fun getForegroundInfo(): ForegroundInfo {
        val n = NotificationCompat.Builder(applicationContext, Notifications.CHANNEL_SYNC)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle("Sending message")
            .setSilent(true)
            .build()
        return ForegroundInfo(FOREGROUND_ID, n)
    }

    companion object {
        private const val TAG = "ReplyWorker"
        private const val KEY_SESSION_ID = "session_id"
        private const val KEY_REPLY = "reply"
        private const val KEY_STATUS = "status"
        private const val KEY_TITLE = "title"
        private const val KEY_BODY = "body"
        private const val FOREGROUND_ID = 4243

        fun enqueue(context: Context, sessionId: String, status: String, title: String, body: String, reply: String) {
            val req = OneTimeWorkRequestBuilder<ReplyWorker>()
                .setInputData(
                    workDataOf(
                        KEY_SESSION_ID to sessionId,
                        "connection_id" to context.container.settings.current.connectionId,
                        KEY_REPLY to reply,
                        KEY_STATUS to status,
                        KEY_TITLE to title,
                        KEY_BODY to body,
                    ),
                )
                .setExpedited(OutOfQuotaPolicy.RUN_AS_NON_EXPEDITED_WORK_REQUEST)
                .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
                .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 10, TimeUnit.SECONDS)
                .build()
            // Appending keeps two quick replies in the order they were typed.
            WorkManager.getInstance(context)
                .enqueueUniqueWork("reply-$sessionId", ExistingWorkPolicy.APPEND_OR_REPLACE, req)
        }
    }
}
