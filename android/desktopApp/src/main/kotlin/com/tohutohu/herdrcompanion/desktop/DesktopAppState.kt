package com.tohutohu.herdrcompanion.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.tohutohu.herdrcompanion.data.Message
import com.tohutohu.herdrcompanion.data.SendState
import com.tohutohu.herdrcompanion.data.api.InteractionResponseDto
import com.tohutohu.herdrcompanion.data.api.MessageDto
import com.tohutohu.herdrcompanion.data.api.Status
import com.tohutohu.herdrcompanion.model.SessionUiModel
import com.tohutohu.herdrcompanion.model.toUiModel
import com.tohutohu.herdrcompanion.ui.detail.AttachmentUiState
import com.tohutohu.herdrcompanion.ui.detail.PendingMessageUiState
import com.tohutohu.herdrcompanion.ui.detail.SessionDetailUiState
import com.tohutohu.herdrcompanion.ui.sessions.SessionListItemUiState
import com.tohutohu.herdrcompanion.ui.sessions.SessionRef
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
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.time.Instant
import java.util.UUID
import java.util.logging.Logger

private const val LIST_POLL_MS = 5_000L
private const val DETAIL_POLL_MS = 3_000L
private const val MAX_UPLOAD_BYTES = 20 * 1024 * 1024

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
    private val connectionSource: DesktopConnectionSource,
    private val api: com.tohutohu.herdrcompanion.data.api.GatewayApi,
    private val http: OkHttpClient,
    private val repository: DesktopGatewayRepository = DesktopGatewayRepository(api),
    initialLayout: DesktopLayoutSnapshot = DesktopLayoutSnapshot(),
    private val onLayoutChanged: (DesktopLayoutSnapshot) -> Unit = {},
) {
    private val logger = Logger.getLogger("com.tohutohu.herdrcompanion.desktop")
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val listMutex = Mutex()
    private val selection = DesktopSelectionState()
    private var listJob: Job? = null
    private var gatewayStartJob: Job? = null
    private val detailJobs = mutableMapOf<String, Job>()
    private var closed = false
    private var pendingBySession by mutableStateOf<Map<String, List<PendingMessageUiState>>>(emptyMap())
    private var pendingAttachments by mutableStateOf<Map<String, List<DesktopAttachment>>>(emptyMap())
    private var pendingMessageBaselines by mutableStateOf<Map<String, Set<String>>>(emptyMap())
    private var composerAttachmentsBySession by mutableStateOf<Map<String, List<DesktopAttachment>>>(emptyMap())
    private var openSessionIdsState by mutableStateOf<List<String>>(emptyList())
    private var detailsBySession by mutableStateOf<Map<String, SessionDetailUiState>>(emptyMap())
    private var previousStatuses = emptyMap<String, String>()

    var connection by mutableStateOf(connectionSource.current)
        private set

    var sessions by mutableStateOf<List<SessionListItemUiState>>(emptyList())
        private set
    var listLoaded by mutableStateOf(false)
        private set
    var listRefreshing by mutableStateOf(false)
        private set
    var listError by mutableStateOf(connection.configIssue)
        private set
    var transientError by mutableStateOf<String?>(null)
        private set
    var busySessionIds by mutableStateOf<Set<String>>(emptySet())
        private set
    var archiveConfirmation by mutableStateOf<SessionUiModel?>(null)
        private set
    var newSessionOpen by mutableStateOf(false)
        private set
    /** Bumped when New Session is requested while its window is already open. */
    var newSessionFocusRequest by mutableStateOf(0)
        private set
    var connectionState by mutableStateOf(
        if (connection.configIssue == null) DesktopConnectionState.CONNECTING else DesktopConnectionState.OFFLINE,
    )
        private set
    var notificationEvent by mutableStateOf<DesktopNotificationEvent?>(null)
        private set
    /** Relative widths of [openSessionIds]; reset to equal whenever the pane count changes. */
    var paneWeights by mutableStateOf<List<Float>>(emptyList())
        private set

    val selectedSessionId: String? get() = selection.selectedId
    val openSessionIds: List<String> get() = openSessionIdsState
    val detail: SessionDetailUiState?
        get() = selectedSessionId?.let(detailsBySession::get)

    fun detailFor(id: String): SessionDetailUiState? = detailsBySession[id]

    val connectionLabel: String
        get() = when {
            listRefreshing && !listLoaded -> "Connecting…"
            else -> when (connectionState) {
                DesktopConnectionState.CONNECTING -> "Connecting…"
                DesktopConnectionState.CONNECTED -> "Connected"
                DesktopConnectionState.RECONNECTING -> "Reconnecting…"
                DesktopConnectionState.OFFLINE -> "Offline"
                DesktopConnectionState.AUTHENTICATION_FAILED -> "Authentication failed"
            }
        }

    val composerAttachmentStates: List<AttachmentUiState>
        get() = composerAttachmentStates(selectedSessionId)

    fun composerAttachmentStates(sessionId: String?): List<AttachmentUiState> =
        sessionId?.let { composerAttachmentsBySession[it].orEmpty() }
            .orEmpty()
            .map { it.toUiState() }

    fun resolveAttachmentPreview(id: String): Any? = (composerAttachmentsBySession.values.flatten() + pendingAttachments.values.flatten())
        .firstOrNull { it.id == id }
        ?.previewModel

    init {
        restoreLayout(initialLayout)
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

    /** Starts the separately installed manager and waits for its config/API to become ready. */
    fun startGateway() {
        if (closed) return
        if (!DesktopPlatformActions.requestGatewayStart()) {
            reportError("Could not open the Herdr Companion Gateway manager.")
            return
        }
        gatewayStartJob?.cancel()
        connectionState = DesktopConnectionState.CONNECTING
        gatewayStartJob = scope.launch {
            repeat(16) {
                delay(500)
                if (closed) return@launch
                refreshSessionsNow(userInitiated = it == 0)
                if (connectionState == DesktopConnectionState.CONNECTED) return@launch
            }
            refreshSessions(userInitiated = true)
        }
    }

    private suspend fun refreshSessionsNow(userInitiated: Boolean) {
        listMutex.withLock {
            connection = connectionSource.reload()
            val showRefreshing = userInitiated || !listLoaded
            val wasLoaded = listLoaded
            if (showRefreshing) listRefreshing = true
            try {
                if (!connection.settings.isConfigured) {
                    listError = connection.configIssue ?: "Gateway configuration is incomplete."
                    connectionState = DesktopConnectionState.OFFLINE
                    return
                }
                val rows = repository.sessions()
                if (wasLoaded) {
                    rows.forEach { row ->
                        notificationForStatusTransition(
                            sessionId = row.id,
                            project = row.title ?: row.project,
                            previous = previousStatuses[row.id],
                            next = row.status,
                        )?.let { notificationEvent = it }
                    }
                }
                previousStatuses = rows.associate { it.id to it.status }
                val open = openSessionIdsState
                sessions = rows.map { row ->
                    SessionListItemUiState(
                        session = row.toUiModel(),
                        relativeUpdatedAt = relativeTime(row.updatedAt),
                        selected = row.id in open,
                    )
                }
                listLoaded = true
                listError = null
                connectionState = DesktopConnectionState.CONNECTED
                open.filterNot { id -> rows.any { it.id == id } }.forEach(::closeSession)
                updateSessionRowSelection()
                logger.fine("session list refreshed: ${rows.size} sessions")
            } catch (cancel: CancellationException) {
                throw cancel
            } catch (error: Exception) {
                val mapped = error.toDesktopGatewayError()
                listError = mapped.message
                connectionState = when (mapped.kind) {
                    DesktopGatewayErrorKind.AUTHENTICATION -> DesktopConnectionState.AUTHENTICATION_FAILED
                    DesktopGatewayErrorKind.GATEWAY_OFFLINE,
                    DesktopGatewayErrorKind.NETWORK,
                    DesktopGatewayErrorKind.STREAMING_DISCONNECTED -> DesktopConnectionState.RECONNECTING
                    else -> DesktopConnectionState.OFFLINE
                }
                logger.warning("session list failed: ${mapped.kind}")
            } finally {
                if (showRefreshing) listRefreshing = false
            }
        }
    }

    /** Opens a session in the last-focused pane, replacing only that pane. */
    fun openSession(id: String) {
        if (closed) return
        val current = openSessionIdsState
        val next = openSessionInFocusedPane(current, selectedSessionId, id)
        current.firstOrNull { it !in next }?.let(::removeDetail)
        setOpenSessionIds(next)
        selection.select(id)
        updateSessionRowSelection()
        ensureDetail(id)
        saveLayout()
    }

    /** Adds a session to the Desktop split view, or focuses it if it is already open. */
    fun openSessionInSplit(id: String) {
        if (closed) return
        if (id in openSessionIdsState) {
            focusSession(id)
            return
        }
        if (openSessionIdsState.isEmpty()) {
            openSession(id)
            return
        }
        setOpenSessionIds(openSessionIdsState + id)
        selection.select(id)
        updateSessionRowSelection()
        ensureDetail(id)
        saveLayout()
    }

    fun focusSession(id: String) {
        if (id !in openSessionIdsState) return
        if (selection.selectedId == id) return
        selection.select(id)
        updateSessionRowSelection()
        saveLayout()
    }

    fun closeSession(id: String) {
        if (id !in openSessionIdsState) return
        val wasSelected = selection.selectedId == id
        removeDetail(id)
        setOpenSessionIds(openSessionIdsState - id)
        if (wasSelected) {
            openSessionIdsState.lastOrNull()?.let(selection::select) ?: selection.clear()
        }
        updateSessionRowSelection()
        saveLayout()
    }

    fun clearSelection() {
        detailJobs.values.forEach(Job::cancel)
        detailJobs.clear()
        detailsBySession = emptyMap()
        cleanupAttachments(composerAttachmentsBySession.values.flatten())
        composerAttachmentsBySession = emptyMap()
        setOpenSessionIds(emptyList())
        selection.clear()
        updateSessionRowSelection()
        saveLayout()
    }

    /** Applies a divider drag; call [saveLayout] when the drag ends. */
    fun updatePaneWeights(weights: List<Float>) {
        if (weights.size == openSessionIdsState.size) paneWeights = weights
    }

    fun saveLayout() {
        if (closed) return
        onLayoutChanged(
            DesktopLayoutSnapshot(
                openSessionIds = openSessionIdsState,
                focusedSessionId = selection.selectedId,
                paneWeights = paneWeights,
            ),
        )
    }

    private fun restoreLayout(layout: DesktopLayoutSnapshot) {
        val restored = layout.normalized()
        if (restored.openSessionIds.isEmpty()) return
        openSessionIdsState = restored.openSessionIds
        paneWeights = restored.paneWeights
        restored.focusedSessionId?.let(selection::select)
        restored.openSessionIds.forEach(::ensureDetail)
    }

    private fun setOpenSessionIds(ids: List<String>) {
        if (ids.size != openSessionIdsState.size) paneWeights = equalPaneWeights(ids.size)
        openSessionIdsState = ids
    }

    private fun ensureDetail(id: String) {
        if (detailJobs[id]?.isActive == true) return
        detailsBySession = detailsBySession + (id to loadingDetail(id))
        detailJobs[id] = scope.launch { detailLoop(id) }
    }

    private fun loadingDetail(id: String) = SessionDetailUiState(
        sessionId = id,
        session = null,
        messages = emptyList(),
        pending = pendingBySession[id].orEmpty(),
        error = null,
        answering = false,
        attachments = composerAttachmentStates(id),
        actionBusy = id in busySessionIds,
        loading = true,
        attachmentsEnabled = false,
        showBackButton = false,
        sendOnEnter = true,
        sendWithModifier = false,
    )

    private fun removeDetail(id: String) {
        detailJobs.remove(id)?.cancel()
        detailsBySession = detailsBySession - id
        cleanupAttachments(composerAttachmentsBySession[id].orEmpty())
        composerAttachmentsBySession = composerAttachmentsBySession - id
    }

    private fun updateSessionRowSelection() {
        val open = openSessionIdsState.toSet()
        sessions = sessions.map { it.copy(selected = it.session.id in open) }
    }

    private suspend fun detailLoop(id: String) {
        var first = true
        var messages = emptyList<Message>()
        var anchor: String? = null
        while (currentCoroutineContext().isActive && !closed && id in openSessionIdsState) {
            try {
                // Keep the explicit GET /sessions/{id} call on the first load;
                // it gives a useful Session-not-found error before messages.
                if (first) repository.session(id)
                val since = anchor.takeUnless { first }
                val response = if (since == null) {
                    repository.messageSnapshot(id)
                } else {
                    repository.changedMessageSnapshot(id, since)
                }
                if (response == null) {
                    // Unchanged; only clear an error left by an earlier poll.
                    if (detailsBySession[id]?.error != null) updateDetail(id) { it.copy(error = null) }
                    delay(DETAIL_POLL_MS)
                    continue
                }
                messages = mergeMessages(messages, response.messages.map(MessageDto::toMessage))
                anchor = messages.lastOrNull()?.id
                settlePending(id, messages)
                updateDetail(id, SessionDetailUiState(
                    sessionId = id,
                    session = response.session.toUiModel(),
                    messages = messages,
                    pending = pendingBySession[id].orEmpty(),
                    error = null,
                    answering = detailsBySession[id]?.answering == true,
                    attachments = composerAttachmentStates(id),
                    actionBusy = id in busySessionIds,
                    loading = false,
                    attachmentsEnabled = true,
                    showBackButton = false,
                    sendOnEnter = true,
                    sendWithModifier = false,
                ))
                first = false
                logger.fine("session detail refreshed")
            } catch (cancel: CancellationException) {
                throw cancel
            } catch (error: Exception) {
                val mapped = error.toDesktopGatewayError(streaming = !first)
                if (id in openSessionIdsState) {
                    val current = detailsBySession[id] ?: loadingDetail(id)
                    updateDetail(id, (current).copy(
                        sessionId = id,
                        messages = messages,
                        pending = pendingBySession[id].orEmpty(),
                        attachments = composerAttachmentStates(id),
                        error = mapped.message,
                        attachmentsEnabled = true,
                        loading = first,
                    ))
                }
                logger.warning("session detail failed: ${mapped.kind}")
            }
            delay(DETAIL_POLL_MS)
        }
    }

    fun addAttachments(paths: List<Path>) {
        selectedSessionId?.let { addAttachments(it, paths) }
    }

    fun addAttachments(sessionId: String, paths: List<Path>) {
        val candidates = paths
            .filter { Files.isRegularFile(it) }
            .map(Path::toDesktopAttachment)
        addDesktopAttachments(sessionId, candidates)
    }

    /** Reads a clipboard image and adds the generated PNG to the composer. */
    fun addClipboardImage(sessionId: String): Boolean {
        val path = DesktopPlatformActions.pasteImage() ?: return false
        addDesktopAttachments(sessionId, listOf(path.toDesktopAttachment(deleteWhenDone = true)))
        return true
    }

    private fun addDesktopAttachments(sessionId: String, candidates: List<DesktopAttachment>) {
        if (sessionId !in openSessionIdsState) {
            cleanupAttachments(candidates)
            return
        }
        val existing = composerAttachmentsBySession[sessionId].orEmpty()
        val additions = candidates
            .filter { Files.isRegularFile(it.path) }
            .filterNot { candidate -> existing.any { it.id == candidate.id } }
        cleanupAttachments(candidates.filterNot { it in additions })
        if (additions.isEmpty()) return
        composerAttachmentsBySession = composerAttachmentsBySession + (sessionId to (existing + additions))
        updateDetail(sessionId) { it.copy(attachments = composerAttachmentStates(sessionId)) }
    }

    fun removeAttachment(id: String) {
        selectedSessionId?.let { removeAttachment(it, id) }
    }

    fun removeAttachment(sessionId: String, attachmentId: String) {
        val current = composerAttachmentsBySession[sessionId].orEmpty()
        val removed = current.filter { it.id == attachmentId }
        val attachments = current
            .filterNot { it.id == attachmentId }
        cleanupAttachments(removed)
        composerAttachmentsBySession = if (attachments.isEmpty()) {
            composerAttachmentsBySession - sessionId
        } else {
            composerAttachmentsBySession + (sessionId to attachments)
        }
        updateDetail(sessionId) { it.copy(attachments = composerAttachmentStates(sessionId)) }
    }

    fun send(text: String) {
        selectedSessionId?.let { send(it, text) }
    }

    fun send(id: String, text: String) {
        if (id !in openSessionIdsState) return
        val attachments = composerAttachmentsBySession[id].orEmpty()
        if ((text.isBlank() && attachments.isEmpty()) || id in busySessionIds) return
        val pending = PendingMessageUiState(
            localId = UUID.randomUUID().toString(),
            text = text,
            attachments = attachments.map { it.toUiState() },
            state = SendState.SENDING,
            error = null,
        )
        pendingAttachments = pendingAttachments + (pending.localId to attachments)
        pendingMessageBaselines = pendingMessageBaselines + (
            pending.localId to detailsBySession[id]?.messages.orEmpty().map(Message::id).toSet()
        )
        updatePending(id) { it + pending }
        composerAttachmentsBySession = composerAttachmentsBySession - id
        updateDetail(id) { it.copy(attachments = emptyList()) }
        scope.launch {
            try {
                val uploads = uploadAttachments(attachments)
                repository.send(id, text, uploads)
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
        selectedSessionId?.let { retry(it, localId) }
    }

    fun retry(id: String, localId: String) {
        val original = pendingBySession[id].orEmpty().firstOrNull { it.localId == localId } ?: return
        if (original.state != SendState.FAILED) return
        updatePending(id) { rows -> rows.map { if (it.localId == localId) it.copy(state = SendState.SENDING, error = null) else it } }
        pendingMessageBaselines = pendingMessageBaselines + (
            localId to detailsBySession[id]?.messages.orEmpty().map(Message::id).toSet()
        )
        sendRetry(id, original, pendingAttachments[localId].orEmpty())
    }

    private fun sendRetry(id: String, original: PendingMessageUiState, attachments: List<DesktopAttachment>) {
        scope.launch {
            try {
                val uploads = uploadAttachments(attachments)
                repository.send(id, original.text, uploads)
                updatePending(id) { rows -> rows.map { if (it.localId == original.localId) it.copy(state = SendState.ACCEPTED) else it } }
            } catch (error: Exception) {
                val mapped = error.toDesktopGatewayError()
                updatePending(id) { rows -> rows.map { if (it.localId == original.localId) it.copy(state = SendState.FAILED, error = mapped.message) else it } }
            }
        }
    }

    fun discard(localId: String) {
        selectedSessionId?.let { discard(it, localId) }
    }

    fun discard(id: String, localId: String) {
        cleanupAttachments(pendingAttachments[localId].orEmpty())
        pendingAttachments = pendingAttachments - localId
        pendingMessageBaselines = pendingMessageBaselines - localId
        updatePending(id) { rows -> rows.filterNot { it.localId == localId } }
    }

    fun respond(response: InteractionResponseDto) {
        selectedSessionId?.let { respond(it, response) }
    }

    fun respond(id: String, response: InteractionResponseDto) {
        val current = detailsBySession[id] ?: return
        if (current.answering) return
        updateDetail(id) { it.copy(answering = true) }
        scope.launch {
            try {
                repository.respond(id, response)
                logger.fine("interaction response accepted by Gateway")
            } catch (error: Exception) {
                val mapped = error.toDesktopGatewayError()
                transientError = mapped.message
                logger.warning("interaction response failed: ${mapped.kind}")
            } finally {
                if (id in openSessionIdsState) updateDetail(id) { it.copy(answering = false) }
            }
        }
    }

    fun cycleMode(id: String) {
        if (id in busySessionIds) return
        setBusy(id, true)
        scope.launch {
            try {
                repository.cycleMode(id)
                logger.fine("session mode change requested")
            } catch (error: Exception) {
                val mapped = error.toDesktopGatewayError()
                reportError(mapped.message)
                logger.warning("session mode change failed: ${mapped.kind}")
            } finally {
                setBusy(id, false)
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
                if (id in openSessionIdsState) closeSession(id)
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

    /** Opens the default terminal app attached to the session's Herdr pane. */
    fun attachTerminal(paneId: String) {
        scope.launch(Dispatchers.IO) {
            DesktopHerdrTerminal.attach(paneId)?.let(::reportError)
        }
    }

    fun reportError(message: String) {
        transientError = message
    }

    fun dismissError() {
        transientError = null
    }

    fun dismissNotification(id: String) {
        if (notificationEvent?.id == id) notificationEvent = null
    }

    fun openNewSession() {
        if (newSessionOpen) newSessionFocusRequest++ else newSessionOpen = true
    }

    fun closeNewSession() {
        newSessionOpen = false
    }

    private fun setBusy(id: String, busy: Boolean) {
        busySessionIds = if (busy) busySessionIds + id else busySessionIds - id
        updateDetail(id) { it.copy(actionBusy = busy) }
    }

    private fun updatePending(id: String, update: (List<PendingMessageUiState>) -> List<PendingMessageUiState>) {
        pendingBySession = pendingBySession + (id to update(pendingBySession[id].orEmpty()))
        updateDetail(id) { it.copy(pending = pendingBySession[id].orEmpty()) }
    }

    private fun updateDetail(id: String, value: SessionDetailUiState) {
        if (id in openSessionIdsState) detailsBySession = detailsBySession + (id to value)
    }

    private fun updateDetail(id: String, update: (SessionDetailUiState) -> SessionDetailUiState) {
        detailsBySession[id]?.let { updateDetail(id, update(it)) }
    }

    private suspend fun uploadAttachments(attachments: List<DesktopAttachment>): List<String> =
        attachments.map { attachment ->
            val bytes = withContext(kotlinx.coroutines.Dispatchers.IO) {
                val size = Files.size(attachment.path)
                if (size > MAX_UPLOAD_BYTES) {
                    error("${attachment.name} is larger than ${MAX_UPLOAD_BYTES / (1024 * 1024)} MB")
                }
                Files.readAllBytes(attachment.path)
            }
            api.upload(bytes, attachment.mimeType, attachment.name)
        }

    private fun settlePending(id: String, messages: List<Message>) {
        val existing = pendingBySession[id].orEmpty()
        val remaining = existing.filterNot { pending ->
            val wanted = normalize(pending.text).take(100)
            val textMatched = wanted.isNotEmpty() && messages.any { message ->
                message.role == "user" && normalize(message.text()).contains(wanted)
            }
            val attachmentOnlyMatched = pending.text.isBlank() && pending.attachments.isNotEmpty() &&
                messages.any { message ->
                    message.role == "user" && message.id !in pendingMessageBaselines[pending.localId].orEmpty()
                }
            textMatched || attachmentOnlyMatched
        }
        if (remaining.size != existing.size) {
            val settledIds = existing.filterNot { it in remaining }.map { it.localId }.toSet()
            cleanupAttachments(settledIds.flatMap { pendingAttachments[it].orEmpty() })
            pendingAttachments = pendingAttachments - settledIds
            pendingMessageBaselines = pendingMessageBaselines - settledIds
            pendingBySession = if (remaining.isEmpty()) pendingBySession - id else pendingBySession + (id to remaining)
        }
    }

    fun close() {
        if (closed) return
        closed = true
        detailJobs.values.forEach(Job::cancel)
        detailJobs.clear()
        cleanupAttachments(composerAttachmentsBySession.values.flatten())
        cleanupAttachments(pendingAttachments.values.flatten())
        listJob?.cancel()
        gatewayStartJob?.cancel()
        scope.cancel()
        http.connectionPool.evictAll()
        http.dispatcher.executorService.shutdown()
    }
}

private fun cleanupAttachments(attachments: Iterable<DesktopAttachment>) {
    attachments.filter { it.deleteWhenDone }.forEach { attachment ->
        runCatching { Files.deleteIfExists(attachment.path) }
    }
}

internal fun openSessionInFocusedPane(
    openSessionIds: List<String>,
    focusedSessionId: String?,
    sessionId: String,
): List<String> {
    if (sessionId in openSessionIds) return openSessionIds
    if (openSessionIds.isEmpty()) return listOf(sessionId)

    val focusedIndex = focusedSessionId
        ?.let(openSessionIds::indexOf)
        ?.takeIf { it >= 0 }
        ?: openSessionIds.lastIndex
    return openSessionIds.toMutableList().also { it[focusedIndex] = sessionId }
}

private fun MessageDto.toMessage() = Message(
    id = id,
    role = role,
    timestamp = runCatching { Instant.parse(timestamp).toEpochMilli() }.getOrDefault(0L),
    blocks = blocks,
    queued = queued,
)

private fun Message.text(): String = blocks.filter { it.type == "text" }.joinToString(" ") { it.text.orEmpty() }

private fun DesktopAttachment.toUiState() = AttachmentUiState(
    id = id,
    name = name,
    mimeType = mimeType,
)

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
