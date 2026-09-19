package com.tohutohu.herdrmobile.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.tohutohu.herdrmobile.data.Message
import com.tohutohu.herdrmobile.data.SendState
import com.tohutohu.herdrmobile.data.api.InteractionResponseDto
import com.tohutohu.herdrmobile.data.api.MessageDto
import com.tohutohu.herdrmobile.model.SessionUiModel
import com.tohutohu.herdrmobile.model.toUiModel
import com.tohutohu.herdrmobile.ui.detail.AttachmentUiState
import com.tohutohu.herdrmobile.ui.detail.PendingMessageUiState
import com.tohutohu.herdrmobile.ui.detail.SessionDetailUiState
import com.tohutohu.herdrmobile.ui.sessions.SessionListItemUiState
import com.tohutohu.herdrmobile.ui.sessions.SessionRef
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import okhttp3.OkHttpClient
import java.time.Duration
import java.time.Instant
import java.util.UUID
import java.util.logging.Logger

private const val LIST_POLL_MS = 5_000L
private const val DETAIL_POLL_MS = 3_000L

/** Selection is intentionally separate from the Android navigation back stack. */
class DesktopSelectionState {
    var selectedId by mutableStateOf<String?>(null)
        private set

    fun select(id: String) {
        selectedId = id
    }

    fun clear() {
        selectedId = null
    }
}

class DesktopAppState(
    val connection: DesktopGatewayConnection,
    private val api: com.tohutohu.herdrmobile.data.api.GatewayApi,
    private val http: OkHttpClient,
    private val repository: DesktopGatewayRepository = DesktopGatewayRepository(api),
) {
    private val logger = Logger.getLogger("com.tohutohu.herdrmobile.desktop")
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val listMutex = Mutex()
    private val selection = DesktopSelectionState()
    private var listJob: Job? = null
    private var detailJob: Job? = null
    private var closed = false
    private var pendingBySession by mutableStateOf<Map<String, List<PendingMessageUiState>>>(emptyMap())

    var sessions by mutableStateOf<List<SessionListItemUiState>>(emptyList())
        private set
    var listLoaded by mutableStateOf(false)
        private set
    var listRefreshing by mutableStateOf(false)
        private set
    var listError by mutableStateOf(connection.configIssue)
        private set
    var detail by mutableStateOf<SessionDetailUiState?>(null)
        private set
    var transientError by mutableStateOf<String?>(null)
        private set
    var busySessionIds by mutableStateOf<Set<String>>(emptySet())
        private set
    var archiveConfirmation by mutableStateOf<SessionUiModel?>(null)
        private set
    var newSessionOpen by mutableStateOf(false)

    val selectedSessionId: String? get() = selection.selectedId
    val connectionLabel: String
        get() = when {
            listRefreshing && !listLoaded -> "Connecting…"
            listError != null && !listLoaded -> "Gateway unavailable"
            listError != null -> "Connection problem"
            else -> "Connected"
        }

    init {
        refreshSessions()
        listJob = scope.launch {
            while (isActive) {
                delay(LIST_POLL_MS)
                refreshSessions()
            }
        }
    }

    fun refreshSessions(userInitiated: Boolean = false) {
        if (closed) return
        scope.launch { refreshSessionsNow(userInitiated) }
    }

    private suspend fun refreshSessionsNow(userInitiated: Boolean) {
        listMutex.withLock {
            val showRefreshing = userInitiated || !listLoaded
            if (showRefreshing) listRefreshing = true
            try {
                if (!connection.settings.isConfigured) {
                    listError = connection.configIssue ?: "Gateway configuration is incomplete."
                    return
                }
                val rows = repository.sessions()
                val selected = selection.selectedId
                sessions = rows.map { row ->
                    SessionListItemUiState(
                        session = row.toUiModel(),
                        relativeUpdatedAt = relativeTime(row.updatedAt),
                        selected = row.id == selected,
                    )
                }
                listLoaded = true
                listError = null
                if (selected != null && rows.none { it.id == selected }) {
                    selection.clear()
                    detailJob?.cancel()
                    detail = null
                }
                logger.fine("session list refreshed: ${rows.size} sessions")
            } catch (cancel: CancellationException) {
                throw cancel
            } catch (error: Exception) {
                val mapped = error.toDesktopGatewayError()
                listError = mapped.message
                logger.warning("session list failed: ${mapped.kind}")
            } finally {
                if (showRefreshing) listRefreshing = false
            }
        }
    }

    fun selectSession(id: String) {
        if (closed || selection.selectedId == id) return
        detailJob?.cancel()
        selection.select(id)
        sessions = sessions.map { it.copy(selected = it.session.id == id) }
        detail = SessionDetailUiState(
            sessionId = id,
            session = null,
            messages = emptyList(),
            pending = pendingBySession[id].orEmpty(),
            error = null,
            answering = false,
            attachments = emptyList(),
            actionBusy = id in busySessionIds,
            loading = true,
            attachmentsEnabled = false,
            showBackButton = false,
            sendOnEnter = true,
        )
        detailJob = scope.launch { detailLoop(id) }
    }

    fun clearSelection() {
        detailJob?.cancel()
        detailJob = null
        selection.clear()
        sessions = sessions.map { it.copy(selected = false) }
        detail = null
    }

    private suspend fun detailLoop(id: String) {
        var first = true
        var messages = emptyList<Message>()
        var anchor: String? = null
        while (currentCoroutineContext().isActive && !closed && selection.selectedId == id) {
            try {
                // Keep the explicit GET /sessions/{id} call on the first load;
                // it gives a useful Session-not-found error before messages.
                if (first) repository.session(id)
                val response = repository.messageSnapshot(id, after = if (first) null else anchor)
                messages = mergeMessages(messages, response.messages.map(MessageDto::toMessage))
                anchor = messages.lastOrNull()?.id
                settlePending(id, messages)
                detail = SessionDetailUiState(
                    sessionId = id,
                    session = response.session.toUiModel(),
                    messages = messages,
                    pending = pendingBySession[id].orEmpty(),
                    error = null,
                    answering = detail?.answering == true,
                    attachments = emptyList(),
                    actionBusy = id in busySessionIds,
                    loading = false,
                    attachmentsEnabled = false,
                    showBackButton = false,
                    sendOnEnter = true,
                )
                first = false
                logger.fine("session detail refreshed")
            } catch (cancel: CancellationException) {
                throw cancel
            } catch (error: Exception) {
                val mapped = error.toDesktopGatewayError(streaming = !first)
                if (selection.selectedId == id) {
                    detail = (detail ?: SessionDetailUiState(
                        sessionId = id,
                        session = null,
                        messages = messages,
                        pending = pendingBySession[id].orEmpty(),
                        error = null,
                        answering = false,
                    )).copy(error = mapped.message, loading = first)
                }
                logger.warning("session detail failed: ${mapped.kind}")
            }
            delay(DETAIL_POLL_MS)
        }
    }

    fun send(text: String) {
        val id = selection.selectedId ?: return
        if (text.isBlank() || id in busySessionIds) return
        val pending = PendingMessageUiState(
            localId = UUID.randomUUID().toString(),
            text = text,
            attachments = emptyList<AttachmentUiState>(),
            state = SendState.SENDING,
            error = null,
        )
        updatePending(id) { it + pending }
        scope.launch {
            try {
                repository.send(id, text)
                updatePending(id) { rows ->
                    rows.map { row -> if (row.localId == pending.localId) row.copy(state = SendState.ACCEPTED) else row }
                }
                logger.fine("message accepted by Gateway")
            } catch (error: Exception) {
                val mapped = error.toDesktopGatewayError()
                updatePending(id) { rows ->
                    rows.map { row -> if (row.localId == pending.localId) row.copy(state = SendState.FAILED, error = mapped.message) else row }
                }
                logger.warning("message send failed: ${mapped.kind}")
            }
        }
    }

    fun retry(localId: String) {
        val id = selection.selectedId ?: return
        val original = pendingBySession[id].orEmpty().firstOrNull { it.localId == localId } ?: return
        if (original.state != SendState.FAILED) return
        updatePending(id) { rows -> rows.map { if (it.localId == localId) it.copy(state = SendState.SENDING, error = null) else it } }
        sendRetry(id, original)
    }

    private fun sendRetry(id: String, original: PendingMessageUiState) {
        scope.launch {
            try {
                repository.send(id, original.text)
                updatePending(id) { rows -> rows.map { if (it.localId == original.localId) it.copy(state = SendState.ACCEPTED) else it } }
            } catch (error: Exception) {
                val mapped = error.toDesktopGatewayError()
                updatePending(id) { rows -> rows.map { if (it.localId == original.localId) it.copy(state = SendState.FAILED, error = mapped.message) else it } }
            }
        }
    }

    fun discard(localId: String) {
        val id = selection.selectedId ?: return
        updatePending(id) { rows -> rows.filterNot { it.localId == localId } }
    }

    fun respond(response: InteractionResponseDto) {
        val id = selection.selectedId ?: return
        val current = detail ?: return
        if (current.answering) return
        detail = current.copy(answering = true)
        scope.launch {
            try {
                repository.respond(id, response)
                logger.fine("interaction response accepted by Gateway")
            } catch (error: Exception) {
                val mapped = error.toDesktopGatewayError()
                transientError = mapped.message
                logger.warning("interaction response failed: ${mapped.kind}")
            } finally {
                if (selection.selectedId == id) detail = detail?.copy(answering = false)
            }
        }
    }

    fun requestArchive(ref: SessionRef) {
        val model = sessions.firstOrNull { it.session.id == ref.id }?.session
            ?: detail?.session?.takeIf { it.id == ref.id }
        if (model == null) {
            reportError("Session not found.")
        } else if (model.live) {
            archiveConfirmation = model
        } else {
            archiveNow(model.id)
        }
    }

    fun confirmArchive() {
        val id = archiveConfirmation?.id ?: return
        archiveConfirmation = null
        archiveNow(id)
    }

    fun cancelArchive() {
        archiveConfirmation = null
    }

    private fun archiveNow(id: String) {
        setBusy(id, true)
        scope.launch {
            try {
                api.archive(id)
                if (selection.selectedId == id) clearSelection()
                refreshSessions()
                logger.info("session archived")
            } catch (error: Exception) {
                val mapped = error.toDesktopGatewayError()
                reportError(mapped.message)
                logger.warning("session archive failed: ${mapped.kind}")
            } finally {
                setBusy(id, false)
            }
        }
    }

    fun unarchive(ref: SessionRef) {
        setBusy(ref.id, true)
        scope.launch {
            try {
                api.unarchive(ref.id)
                refreshSessions()
            } catch (error: Exception) {
                val mapped = error.toDesktopGatewayError()
                reportError(mapped.message)
                logger.warning("session unarchive failed: ${mapped.kind}")
            } finally {
                setBusy(ref.id, false)
            }
        }
    }

    fun resume(ref: SessionRef) {
        setBusy(ref.id, true)
        scope.launch {
            try {
                api.resume(ref.id, trust = true)
                refreshSessions()
                logger.info("session resume requested")
            } catch (error: Exception) {
                val mapped = error.toDesktopGatewayError()
                reportError(mapped.message)
                logger.warning("session resume failed: ${mapped.kind}")
            } finally {
                setBusy(ref.id, false)
            }
        }
    }

    fun reportError(message: String) {
        transientError = message
    }

    fun dismissError() {
        transientError = null
    }

    fun openNewSession() {
        newSessionOpen = true
    }

    fun closeNewSession() {
        newSessionOpen = false
    }

    private fun setBusy(id: String, busy: Boolean) {
        busySessionIds = if (busy) busySessionIds + id else busySessionIds - id
        detail = detail?.takeIf { it.sessionId == id }?.copy(actionBusy = busy)
    }

    private fun updatePending(id: String, update: (List<PendingMessageUiState>) -> List<PendingMessageUiState>) {
        pendingBySession = pendingBySession + (id to update(pendingBySession[id].orEmpty()))
        if (selection.selectedId == id) detail = detail?.copy(pending = pendingBySession[id].orEmpty())
    }

    private fun settlePending(id: String, messages: List<Message>) {
        val remaining = pendingBySession[id].orEmpty().filterNot { pending ->
            val wanted = normalize(pending.text).take(100)
            wanted.isNotEmpty() && messages.any { message ->
                message.role == "user" && normalize(message.text()).contains(wanted)
            }
        }
        if (remaining.size != pendingBySession[id].orEmpty().size) {
            pendingBySession = if (remaining.isEmpty()) pendingBySession - id else pendingBySession + (id to remaining)
        }
    }

    fun close() {
        if (closed) return
        closed = true
        detailJob?.cancel()
        listJob?.cancel()
        scope.cancel()
        http.connectionPool.evictAll()
        http.dispatcher.executorService.shutdown()
    }
}

private fun MessageDto.toMessage() = Message(
    id = id,
    role = role,
    timestamp = runCatching { Instant.parse(timestamp).toEpochMilli() }.getOrDefault(0L),
    blocks = blocks,
    queued = queued,
)

private fun Message.text(): String = blocks.filter { it.type == "text" }.joinToString(" ") { it.text.orEmpty() }

private fun normalize(value: String): String = value.replace(Regex("\\s+"), " ").trim()

private fun mergeMessages(old: List<Message>, incoming: List<Message>): List<Message> {
    val merged = LinkedHashMap<String, Message>()
    old.forEach { merged[it.id] = it }
    incoming.forEach { merged[it.id] = it }
    return merged.values.toList()
}

private fun relativeTime(value: String): String {
    val then = runCatching { Instant.parse(value) }.getOrNull() ?: return ""
    val seconds = Duration.between(then, Instant.now()).seconds.coerceAtLeast(0)
    return when {
        seconds < 60 -> "just now"
        seconds < 3_600 -> "${seconds / 60}m ago"
        seconds < 86_400 -> "${seconds / 3_600}h ago"
        else -> "${seconds / 86_400}d ago"
    }
}
