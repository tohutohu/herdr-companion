package com.tohutohu.herdrmobile.ui.detail

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.tohutohu.herdrmobile.container
import com.tohutohu.herdrmobile.data.Attachment
import com.tohutohu.herdrmobile.data.Message
import com.tohutohu.herdrmobile.data.PendingMessage
import com.tohutohu.herdrmobile.data.matchPending
import com.tohutohu.herdrmobile.data.api.GatewayException
import com.tohutohu.herdrmobile.data.api.InteractionResponseDto
import com.tohutohu.herdrmobile.data.db.SessionEntity
import com.tohutohu.herdrmobile.push.Notifications
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch

private const val POLL_MS = 3_000L

class SessionDetailViewModel(app: Application, val sessionId: String) : AndroidViewModel(app) {
    private val repo = app.container.repository
    private val outbox = app.container.outbox

    val session: StateFlow<SessionEntity?> =
        repo.observeSession(sessionId).stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), null)
    val messages: StateFlow<List<Message>> =
        repo.observeMessages(sessionId).stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), emptyList())

    /**
     * Messages sent from the app that the conversation does not show yet,
     * oldest first. One the agent has queued is shown by the conversation
     * itself (marked as queued), so it is left out here.
     */
    val pending: StateFlow<List<PendingMessage>> =
        combine(messages, outbox.observe(sessionId)) { m, p ->
            val found = matchPending(p, m)
            p.filter { it.localId !in found }
        }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(5_000), emptyList())

    init {
        viewModelScope.launch { messages.collect { outbox.settle(sessionId, it) } }
    }

    private val _error = MutableStateFlow<String?>(null)
    val error = _error.asStateFlow()

    /** An answer to a question or approval is on its way. */
    private val _answering = MutableStateFlow(false)
    val answering = _answering.asStateFlow()

    private var fullSyncDone = false

    /**
     * Called in a loop while the screen is started (foreground only). The
     * session's notification is cleared on entering and on leaving, but not
     * the moment one arrives while the user is reading, so it can still be
     * noticed.
     */
    suspend fun pollWhileVisible() {
        Notifications.cancel(getApplication(), sessionId)
        try {
            while (true) {
                refresh()
                delay(POLL_MS)
            }
        } finally {
            Notifications.cancel(getApplication(), sessionId)
        }
    }

    suspend fun refresh() {
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
}
