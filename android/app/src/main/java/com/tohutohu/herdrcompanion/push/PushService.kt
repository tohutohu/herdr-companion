package com.tohutohu.herdrcompanion.push

import android.content.Context
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
import androidx.core.app.NotificationCompat
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage
import com.tohutohu.herdrcompanion.R
import com.tohutohu.herdrcompanion.container
import com.tohutohu.herdrcompanion.data.api.GatewayException
import java.util.concurrent.TimeUnit
import android.util.Log
import kotlinx.coroutines.launch

/**
 * Gateway pushes are data-only, high priority messages:
 * {sessionId, status, title, body, canSend}. We show the notification ourselves and
 * prefetch the conversation so it is readable immediately on tap.
 */
class PushService : FirebaseMessagingService() {
    override fun onMessageReceived(message: RemoteMessage) {
        val data = message.data
        val configured = container.settings.current
        if (configured.gatewayId.isNotEmpty() && data["gatewayId"] != configured.gatewayId) return
        val sessionId = data["sessionId"] ?: return
        val status = data["status"].orEmpty()
        Notifications.show(
            this,
            sessionId = sessionId,
            status = status,
            title = data["title"] ?: "Herdr Mobile",
            body = data["body"].orEmpty(),
            canSend = data["canSend"] == "true",
        )
        // Mark the cached row immediately so the list can show the unread
        // state even while the background prefetch is still running.
        container.scope.launch { container.repository.markUnread(sessionId) }
        PrefetchWorker.enqueue(this, sessionId)
    }

    override fun onNewToken(token: String) {
        container.pushRegistration.register(token)
    }
}

/** Fetches a session's messages into Room after a push. */
class PrefetchWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    override suspend fun doWork(): Result {
        if (inputData.getString("connection_id").orEmpty() != applicationContext.container.settings.current.connectionId) return Result.failure()
        val sessionId = inputData.getString(KEY_SESSION_ID) ?: return Result.failure()
        val repo = applicationContext.container.repository
        return try {
            repo.refreshMessages(sessionId)
            runCatching { repo.refreshSessions() }
            // The row may not have existed when the FCM callback marked it.
            repo.markUnread(sessionId)
            Result.success()
        } catch (e: GatewayException) {
            Log.w(TAG, "prefetch failed for $sessionId: ${e.code} ${e.message}")
            // Client errors (unknown session, bad token) will not fix themselves.
            if (e.code in 400..499 || runAttemptCount >= 3) Result.failure() else Result.retry()
        } catch (e: Exception) {
            Log.w(TAG, "prefetch failed for $sessionId", e)
            if (runAttemptCount < 3) Result.retry() else Result.failure()
        }
    }

    // Required for expedited work on Android 11 and lower.
    override suspend fun getForegroundInfo(): ForegroundInfo {
        val n = NotificationCompat.Builder(applicationContext, Notifications.CHANNEL_SYNC)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle("Syncing session")
            .setSilent(true)
            .build()
        return ForegroundInfo(FOREGROUND_ID, n)
    }

    companion object {
        private const val TAG = "PrefetchWorker"
        private const val KEY_SESSION_ID = "session_id"
        private const val FOREGROUND_ID = 4242

        fun enqueue(context: Context, sessionId: String) {
            val req = OneTimeWorkRequestBuilder<PrefetchWorker>()
                .setInputData(workDataOf(KEY_SESSION_ID to sessionId, "connection_id" to context.container.settings.current.connectionId))
                .setExpedited(OutOfQuotaPolicy.RUN_AS_NON_EXPEDITED_WORK_REQUEST)
                .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
                .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 10, TimeUnit.SECONDS)
                .build()
            WorkManager.getInstance(context)
                .enqueueUniqueWork("prefetch-$sessionId", ExistingWorkPolicy.REPLACE, req)
        }
    }
}
