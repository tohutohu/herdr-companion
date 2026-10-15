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
import androidx.work.Data
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
import java.io.File
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

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
        val pending = goAsync()
        context.container.scope.launch(Dispatchers.IO) {
            try {
                ReplyWorker.enqueue(context, sessionId, status, title, body, reply)
            } catch (cancel: CancellationException) {
                throw cancel
            } catch (error: Exception) {
                Log.w("ReplyReceiver", "could not queue reply", error)
                Notifications.showReply(context, sessionId, status, title, body, reply, error = "Could not queue the reply. Open the conversation to send it.")
            } finally {
                pending.finish()
            }
        }
    }
}

/**
 * Sends one notification reply. It runs as work rather than inside the
 * receiver so a gateway that is momentarily unreachable gets another try.
 */
class ReplyWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    override suspend fun doWork(): Result {
        val store = payloadStore(applicationContext)
        // Work created by an older APK contains the text in Data. Its previous
        // send outcome is unknown, so migrate it without automatically resending.
        val id = runCatching {
            inputData.getString(KEY_PAYLOAD_ID) ?: legacyReplyPayload(inputData, this.id.toString())
                ?.let { store.create(it).id }
        }.getOrElse {
            Log.w(TAG, "could not migrate queued reply")
            return if (runAttemptCount < 3) Result.retry() else Result.success()
        } ?: return Result.success()
        var payload = runCatching { store.load(id) }.getOrElse {
            Log.w(TAG, "could not read saved reply")
            // Keep the file even after bounded retries. Scheduler success is
            // not proof of delivery; a later app start can recover this read.
            return if (runAttemptCount < 3) Result.retry() else Result.success()
        } ?: return Result.success()
        if (payload.connectionId != applicationContext.container.settings.current.connectionId) {
            store.delete(id)
            return Result.success()
        }
        if (payload.terminal) return Result.success()
        val api = applicationContext.container.api
        return try {
            val supportsReceipts = api.supportsMessageIdempotency()
            if (resumedReplyDecision(payload, supportsReceipts) == ReplyDecision.UNCONFIRMED) {
                return finish(store, payload, unconfirmed = true)
            }
            // Persist BEFORE making the POST. Process death must not cause an
            // old gateway (which ignores requestId) to execute the message twice.
            val attempting = payload.copy(attempted = true, safeToRetry = supportsReceipts)
            store.save(attempting)
            payload = attempting
            api.sendMessage(payload.sessionId, payload.reply, emptyList(), payload.id.takeIf { supportsReceipts })
            store.delete(id)
            Notifications.showReply(applicationContext, payload.sessionId, payload.status, payload.title, payload.body, payload.reply)
            Result.success()
        } catch (cancel: CancellationException) {
            throw cancel
        } catch (error: Exception) {
            val code = (error as? GatewayException)?.code
            Log.w(TAG, "reply delivery failed (${code ?: "transport"})")
            when (failedReplyDecision(payload, code, runAttemptCount)) {
                ReplyDecision.RETRY -> Result.retry()
                ReplyDecision.UNCONFIRMED -> finish(store, payload, unconfirmed = true)
                else -> finish(store, payload, error = error.message ?: "Could not send the reply")
            }
        }
    }

    private fun finish(store: ReplyPayloadStore, payload: ReplyPayload, unconfirmed: Boolean = false, error: String? = null): Result {
        runCatching { store.save(payload.copy(terminal = true)) }
            .onFailure { Log.w(TAG, "could not save terminal reply state") }
        runCatching {
            Notifications.showReply(applicationContext, payload.sessionId, payload.status, payload.title, payload.body,
                payload.reply, error = error ?: "Check the conversation before sending again.", uncertain = unconfirmed)
        }.onFailure { Log.w(TAG, "could not show reply delivery result") }
        // A user-visible failure is terminal for this payload, not for later
        // APPEND work. Returning failure would poison replies already queued.
        return Result.success()
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
        private const val KEY_PAYLOAD_ID = "reply_payload_id"
        private val enqueueMutex = Mutex()
        private const val FOREGROUND_ID = 4243

        private fun payloadStore(context: Context) = ReplyPayloadStore(File(context.filesDir, "notification-replies"))

        suspend fun enqueue(context: Context, sessionId: String, status: String, title: String, body: String, reply: String) {
            enqueueMutex.withLock {
                val store = payloadStore(context)
                store.cleanup(context.container.settings.current.connectionId)
                val payload = store.create(ReplyPayload(connectionId = context.container.settings.current.connectionId,
                    sessionId = sessionId, status = status, title = title, body = body, reply = reply))
                Notifications.showReply(context, sessionId, status, title, body, reply, sending = true)
                enqueuePayload(context, payload)
            }
        }

        /** Recover the persistence/enqueue gap after process death, without adding duplicate work. */
        suspend fun resumePending(context: Context) = enqueueMutex.withLock {
            val store = payloadStore(context)
            val connection = context.container.settings.current.connectionId
            store.cleanup(connection)
            store.pendingIds(connection).forEach { id ->
                val payload = store.load(id) ?: return@forEach
                val known = WorkManager.getInstance(context).getWorkInfosByTag(tag(payload.id)).get()
                when {
                    known.any { it.state == androidx.work.WorkInfo.State.CANCELLED } -> store.save(payload.copy(terminal = true))
                    shouldResumeSavedReply(known.map { it.state }) -> enqueuePayload(context, payload)
                }
            }
        }

        private fun tag(id: String) = "reply-payload-$id"

        private fun enqueuePayload(context: Context, payload: ReplyPayload) {
            WorkManager.getInstance(context)
                .enqueueUniqueWork("reply-${payload.connectionId}-${payload.sessionId}", ExistingWorkPolicy.APPEND_OR_REPLACE, workRequest(payload.id))
                .result.get()
        }

        internal fun workRequest(payloadId: String) = OneTimeWorkRequestBuilder<ReplyWorker>()
                .setInputData(workData(payloadId))
                .addTag(tag(payloadId))
                .setExpedited(OutOfQuotaPolicy.RUN_AS_NON_EXPEDITED_WORK_REQUEST)
                .setConstraints(Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build())
                .setBackoffCriteria(BackoffPolicy.EXPONENTIAL, 10, TimeUnit.SECONDS)
                .build()

        internal fun workData(payloadId: String) = workDataOf(KEY_PAYLOAD_ID to payloadId)
    }
}

/** A remaining payload can be undelivered even when its previous Work succeeded. */
internal fun shouldResumeSavedReply(states: List<androidx.work.WorkInfo.State>): Boolean =
    states.none { it == androidx.work.WorkInfo.State.CANCELLED } && states.all { it.isFinished }

internal fun legacyReplyPayload(data: Data, id: String): ReplyPayload? {
    val session = data.getString("session_id") ?: return null
    val reply = data.getString("reply") ?: return null
    return ReplyPayload(id = id, connectionId = data.getString("connection_id").orEmpty(), sessionId = session,
        status = data.getString("status").orEmpty(), title = data.getString("title").orEmpty(),
        body = data.getString("body").orEmpty(), reply = reply, attempted = true)
}
