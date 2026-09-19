package com.tohutohu.herdrmobile.data

import android.content.ContentResolver
import com.tohutohu.herdrmobile.data.api.GatewayApi
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap

private const val MAX_UPLOAD_BYTES = 20 * 1024 * 1024

/** How long a message the gateway took may wait to show up before it is let go. */
private const val UNSEEN_EXPIRY_MS = 30L * 60 * 1000

/** Leading characters compared when looking for a sent message in the conversation. */
private const val MATCH_CHARS = 100

/** A message sent from the app that the conversation does not show yet. */
data class PendingMessage(
    val localId: String,
    val sessionId: String,
    val text: String,
    val attachments: List<Attachment>,
    val state: SendState,
    val error: String? = null,
    /** Ids already in the conversation when it was sent; none of them can be it. */
    val known: Set<String>,
    val acceptedAt: Long? = null,
    /** Kept after a successful upload so a retry does not upload again. */
    val uploadIds: List<String>? = null,
)

/**
 * Sends messages outside any screen, so leaving the conversation does not
 * cancel a send, and keeps each one on show until the conversation has it.
 * Held in memory only: a message the gateway took is in the agent's hands.
 */
class Outbox(
    private val resolver: ContentResolver,
    private val api: GatewayApi,
    private val repository: SessionRepository,
    private val scope: CoroutineScope,
) {
    private val pending = MutableStateFlow<List<PendingMessage>>(emptyList())

    /** Sends to one session go out one at a time, in the order they were written. */
    private val locks = ConcurrentHashMap<String, Mutex>()

    fun observe(sessionId: String): Flow<List<PendingMessage>> =
        pending.map { all -> all.filter { it.sessionId == sessionId } }.distinctUntilChanged()

    fun send(sessionId: String, text: String, attachments: List<Attachment>, known: Set<String>) {
        val p = PendingMessage(
            localId = UUID.randomUUID().toString(),
            sessionId = sessionId,
            text = text,
            attachments = attachments,
            state = SendState.SENDING,
            known = known,
        )
        pending.update { it + p }
        deliver(p.localId)
    }

    fun retry(localId: String) {
        if (edit(localId) { it.copy(state = SendState.SENDING, error = null) } != null) deliver(localId)
    }

    fun discard(localId: String) {
        pending.update { all -> all.filterNot { it.localId == localId } }
    }

    /**
     * Lets go of the messages the conversation now has for real, and of those
     * the gateway took long ago without them ever showing up.
     */
    fun settle(sessionId: String, messages: List<Message>) {
        pending.update { all -> stillPending(all, sessionId, messages, System.currentTimeMillis()) }
    }

    private fun deliver(localId: String) {
        scope.launch {
            val sessionId = pending.value.firstOrNull { it.localId == localId }?.sessionId ?: return@launch
            locks.getOrPut(sessionId) { Mutex() }.withLock {
                val p = pending.value.firstOrNull { it.localId == localId } ?: return@withLock
                try {
                    val ids = p.uploadIds ?: p.attachments.map { upload(it) }
                    edit(localId) { it.copy(uploadIds = ids) }
                    api.sendMessage(sessionId, p.text, ids)
                    edit(localId) { it.copy(state = SendState.ACCEPTED, acceptedAt = System.currentTimeMillis()) }
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    edit(localId) { it.copy(state = SendState.FAILED, error = e.message ?: e.toString()) }
                    return@withLock
                }
            }
            // The agent writes it down right away, or queues it; pick that up
            // sooner than the next poll would.
            delay(500)
            runCatching { repository.refreshMessages(sessionId) }
        }
    }

    private suspend fun upload(attachment: Attachment): String {
        val bytes = withContext(Dispatchers.IO) {
            resolver.openInputStream(attachment.uri)?.use { it.readBytes() }
                ?: error("cannot read ${attachment.name}")
        }
        // The gateway rejects anything larger, with a much vaguer message.
        if (bytes.size > MAX_UPLOAD_BYTES) {
            error("${attachment.name} is larger than ${MAX_UPLOAD_BYTES / (1024 * 1024)} MB")
        }
        return api.upload(bytes, attachment.mime, attachment.name)
    }

    private fun edit(localId: String, change: (PendingMessage) -> PendingMessage): PendingMessage? {
        var out: PendingMessage? = null
        pending.update { all -> all.map { if (it.localId == localId) change(it).also { c -> out = c } else it } }
        return out
    }
}

/** [all] without the messages of [sessionId] that [settle][Outbox.settle] lets go of. */
internal fun stillPending(all: List<PendingMessage>, sessionId: String, messages: List<Message>, now: Long): List<PendingMessage> {
    val mine = all.filter { it.sessionId == sessionId }
    val arrived = matchPending(mine, messages).filterValues { !it.queued }.keys
    return all.filterNot {
        it.sessionId == sessionId && (it.localId in arrived ||
            it.state == SendState.ACCEPTED && now - (it.acceptedAt ?: now) > UNSEEN_EXPIRY_MS)
    }
}

/**
 * Finds each pending message in the conversation: the first user message
 * that was not there when it was sent, is not taken by an earlier pending
 * one, and carries its text. The agent may add to the text (attachment paths)
 * and reflow whitespace, so only the start is compared, loosely. A message of
 * attachments alone takes the next new user message.
 *
 * Any state is matched: a send whose answer was lost on the way back can
 * still have reached the agent.
 */
fun matchPending(pending: List<PendingMessage>, messages: List<Message>): Map<String, Message> {
    val taken = mutableSetOf<String>()
    val out = mutableMapOf<String, Message>()
    for (p in pending) {
        val want = normalize(p.text).take(MATCH_CHARS)
        val hit = messages.firstOrNull { m ->
            m.role == "user" && m.id !in p.known && m.id !in taken &&
                (want.isEmpty() || normalize(m.text()).contains(want))
        } ?: continue
        taken += hit.id
        out[p.localId] = hit
    }
    return out
}

private fun Message.text(): String = blocks.filter { it.type == "text" }.joinToString(" ") { it.text.orEmpty() }

private fun normalize(s: String): String = s.replace(Regex("\\s+"), " ").trim()
