package com.tohutohu.herdrcompanion.ui.detail

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.tohutohu.herdrcompanion.container
import com.tohutohu.herdrcompanion.data.Attachment
import com.tohutohu.herdrcompanion.data.Message
import com.tohutohu.herdrcompanion.data.PendingMessage
import com.tohutohu.herdrcompanion.data.matchPending
import com.tohutohu.herdrcompanion.data.api.GatewayException
import com.tohutohu.herdrcompanion.data.api.InteractionResponseDto
import com.tohutohu.herdrcompanion.data.db.SessionEntity
import com.tohutohu.herdrcompanion.push.Notifications
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.onEach
import kotlinx.coroutines.flow.scan
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

private const val POLL_MS = 3_000L

data class SessionMessagePresentation(
    val messages: List<Message>,
    val pending: List<PendingMessage>,
    val messageKeys: Map<String, String>,
)

private data class RawMessagePresentation(
    val pending: List<PendingMessage>,
    val messageKeys: Map<String, String>,
    val messageIds: Set<String>,
    val messages: List<Message>,
)

class SessionDetailViewModel(app: Application, val sessionId: String) : AndroidViewModel(app) {
    private val repo = app.container.repository
    private val outbox = app.container.outbox

    val session: StateFlow<SessionEntity?> =
        repo.observeSession(sessionId).stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), null)
    val messages: StateFlow<List<Message>> =
        repo.observeMessages(sessionId).stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), emptyList())

    /**
     * Keeps the local list-item key after a pending message arrives from the
     * gateway. The outbox can forget the pending row immediately, but the
     * visible row must keep its identity or animateItem treats it as a new
     * message and briefly fades it out and back in. The pending portion is
     * oldest first; once the agent queues it, the conversation's queued row
     * replaces it.
     */
    val messagePresentation: StateFlow<SessionMessagePresentation> =
        combine(messages, outbox.observe(sessionId)) { m, p ->
            val found = matchPending(p, m)
            RawMessagePresentation(
                pending = p.filter { it.localId !in found },
                messageKeys = found.entries.associate { (localId, message) ->
                    message.id to "pending:$localId"
                },
                messageIds = m.mapTo(HashSet()) { it.id },
                messages = m,
            )
        }.scan(SessionMessagePresentation(emptyList(), emptyList(), emptyMap())) { previous, current ->
            SessionMessagePresentation(
                messages = current.messages,
                pending = current.pending,
                // Keep aliases for messages still in the conversation. This
                // survives the outbox removing the matched pending row.
                messageKeys = previous.messageKeys.filterKeys { it in current.messageIds } + current.messageKeys,
            )
        // Settle after the match has been folded so its item-key alias is
        // recorded before the outbox removes the pending row.
        }.onEach { outbox.settle(sessionId, it.messages) }
            .stateIn(
                viewModelScope,
                SharingStarted.WhileSubscribed(5_000),
                SessionMessagePresentation(emptyList(), emptyList(), emptyMap()),
            )

    private val _error = MutableStateFlow<String?>(null)
    val error = _error.asStateFlow()

    private val refreshMutex = Mutex()
    private val _loading = MutableStateFlow(true)
    val loading = _loading.asStateFlow()

    /** An answer to a question or approval is on its way. */
    private val _answering = MutableStateFlow(false)
    val answering = _answering.asStateFlow()

    /** A live agent TUI is moving to its next available session mode. */
    private val _modeChanging = MutableStateFlow(false)
    val modeChanging = _modeChanging.asStateFlow()

    private var fullSyncDone = false

    /**
     * Called in a loop while the screen is started (foreground only). The
     * session's notification is cleared on entering and on leaving, but not
     * the moment one arrives while the user is reading, so it can still be
     * noticed.
     */
    suspend fun pollWhileVisible() {
        repo.markRead(sessionId)
        Notifications.cancel(getApplication(), sessionId)
        try {
            while (true) {
                refresh()
                // A push can arrive while this screen is open. The current
                // conversation is still the one the user is reading, so do
                // not leave an unread marker behind when returning to the list.
                repo.markRead(sessionId)
                delay(POLL_MS)
            }
        } finally {
            Notifications.cancel(getApplication(), sessionId)
        }
    }

    suspend fun refresh() {
        refreshMutex.withLock {
            _loading.value = true
            try {
                _error.value = try {
                    // The first fetch after opening replaces the cache entirely.
                    repo.refreshMessages(sessionId, full = !fullSyncDone)
                    fullSyncDone = true
                    null
                } catch (e: GatewayException) {
                    "${e.message}"
                } catch (e: Exception) {
                    "Offline: showing cached messages (${e.message})"
                }
            } finally {
                _loading.value = false
            }
        }
    }

    /** Shown at once and sent in the background; the screen may be left right away. */
    fun send(text: String, attachments: List<Attachment>) {
        outbox.send(sessionId, text, attachments, messages.value.mapTo(HashSet()) { it.id })
    }

    fun retry(localId: String) = outbox.retry(localId)

    fun discard(localId: String) = outbox.discard(localId)

    fun respond(response: InteractionResponseDto) {
        viewModelScope.launch {
            _answering.value = true
            try {
                repo.respond(sessionId, response)
                delay(800)
                refresh()
            } catch (e: Exception) {
                _error.value = "Answer failed: ${e.message}"
            } finally {
                _answering.value = false
            }
        }
    }

    fun cycleMode() {
        if (_modeChanging.value) return
        viewModelScope.launch {
            _modeChanging.value = true
            try {
                repo.cycleMode(sessionId)
                // The mode is reported by the provider's next transcript
                // snapshot; give its TUI a moment to process the shortcut.
                delay(300)
                refresh()
            } catch (e: Exception) {
                _error.value = "Mode change failed: ${e.message}"
            } finally {
                _modeChanging.value = false
            }
        }
    }
}
