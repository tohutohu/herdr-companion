package com.tohutohu.herdrcompanion.desktop

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.tohutohu.herdrcompanion.data.Message
import com.tohutohu.herdrcompanion.data.SendState
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import com.tohutohu.herdrcompanion.data.api.InteractionResponseDto
import com.tohutohu.herdrcompanion.data.api.MessageDto
import com.tohutohu.herdrcompanion.data.api.MessageSnapshot
import com.tohutohu.herdrcompanion.data.api.MessageDeliveryAttempt
import com.tohutohu.herdrcompanion.data.api.UnconfirmedMessageDeliveryException
import com.tohutohu.herdrcompanion.data.api.MessagesResponse
import com.tohutohu.herdrcompanion.model.SessionUiModel
import com.tohutohu.herdrcompanion.model.toUiModel
import com.tohutohu.herdrcompanion.ui.detail.AttachmentUiState
import com.tohutohu.herdrcompanion.ui.detail.PendingMessageUiState
import com.tohutohu.herdrcompanion.ui.detail.ReloadNotice
import com.tohutohu.herdrcompanion.ui.detail.SessionDetailUiState
import com.tohutohu.herdrcompanion.ui.detail.pendingInteractionIds
import com.tohutohu.herdrcompanion.ui.detail.reloadNotice
import com.tohutohu.herdrcompanion.ui.sessions.ArchivedSessionsPager
import com.tohutohu.herdrcompanion.ui.sessions.SessionListItemUiState
import com.tohutohu.herdrcompanion.ui.sessions.SessionRef
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import okhttp3.OkHttpClient
import java.awt.Rectangle
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.time.Instant
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.logging.Logger

private const val LIST_POLL_MS = 5_000L
private const val DETAIL_POLL_MS = 3_000L
private const val RELOAD_NOTICE_MS = 4_000L

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
    private val onLayoutChanged: (DesktopLayoutSnapshot) -> Unit = {},
) {
    private val logger = Logger.getLogger("com.tohutohu.herdrcompanion.desktop")
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val listMutex = Mutex()
    private val selection = DesktopSelectionState()
    private var listJob: Job? = null
    private var gatewayStartJob: Job? = null
    private val detailJobs = mutableMapOf<String, Job>()
    private val reloadSignals = ConcurrentHashMap<String, Channel<Unit>>()
    private var closed = false
    private var pendingBySession by mutableStateOf<Map<String, List<PendingMessageUiState>>>(emptyMap())
    private var pendingAttachments by mutableStateOf<Map<String, List<DesktopAttachment>>>(emptyMap())
    private val pendingDeliveries = ConcurrentHashMap<String, DesktopPendingDelivery>()
    private val deliverySessionLocks = ConcurrentHashMap<String, Mutex>()
    private var pendingMessageBaselines by mutableStateOf<Map<String, Set<String>>>(emptyMap())
    private var composerAttachmentsBySession by mutableStateOf<Map<String, List<DesktopAttachment>>>(emptyMap())
    private var openSessionIdsState by mutableStateOf<List<String>>(emptyList())
    private var detachedWindowsState by mutableStateOf<List<DesktopDetachedWindow>>(emptyList())
    private var detailsBySession by mutableStateOf<Map<String, SessionDetailUiState>>(emptyMap())
    private var previousStatuses = emptyMap<String, String>()
    private val archivedPager = ArchivedSessionsPager { offset, limit -> repository.archivedSessions(offset, limit) }

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
    /** Something the user should know that is not an error, e.g. a worktree that was kept. */
    var notice by mutableStateOf<String?>(null)
        private set
    var busySessionIds by mutableStateOf<Set<String>>(emptySet())
        private set
    /** Sessions waiting for the stop-and-archive confirmation; empty when none is asked. */
    var archiveConfirmation by mutableStateOf<List<SessionUiModel>>(emptyList())
        private set

    /** The sidebar lists archived sessions instead of the current ones. */
    var showingArchived by mutableStateOf(false)
        private set
    /** Archived sessions loaded so far, read from the gateway only while browsing them. */
    var archivedSessions by mutableStateOf<List<SessionListItemUiState>>(emptyList())
        private set
    var archivedRefreshing by mutableStateOf(false)
        private set
    var archivedError by mutableStateOf<String?>(null)
        private set
    val archivedLoaded: Boolean get() = archivedPager.sessions != null
    val archivedHasMore: Boolean get() = archivedPager.hasMore
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

    /** Sessions shown in their own windows rather than in the main window's panes. */
    val detachedWindows: List<DesktopDetachedWindow> get() = detachedWindowsState

    /** Bumped to bring a detached session's window to the front. */
    var windowRaiseRequest by mutableStateOf<Pair<String, Int>?>(null)
        private set
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
        if (userInitiated && showingArchived) reloadArchived(keepLoaded = false)
    }

    fun showArchived(show: Boolean) {
        if (closed || show == showingArchived) return
        showingArchived = show
        if (show) reloadArchived(keepLoaded = false)
    }

    /** Reads the next page of archived sessions, if there is one. */
    fun loadMoreArchived() {
        if (closed || !archivedPager.hasMore) return
        scope.launch {
            try {
                archivedPager.loadMore()
                updateArchivedRows()
            } catch (cancel: CancellationException) {
                throw cancel
            } catch (error: Exception) {
                val mapped = error.toDesktopGatewayError()
                reportError("Could not load more archived sessions: ${mapped.message}")
                logger.warning("archived sessions page failed: ${mapped.kind}")
            }
        }
    }

    private fun reloadArchived(keepLoaded: Boolean) {
        scope.launch {
            archivedRefreshing = true
            try {
                archivedPager.load(keepLoaded)
                archivedError = null
                updateArchivedRows()
            } catch (cancel: CancellationException) {
                throw cancel
            } catch (error: Exception) {
                val mapped = error.toDesktopGatewayError()
                archivedError = mapped.message
                logger.warning("archived sessions failed: ${mapped.kind}")
            } finally {
                archivedRefreshing = false
            }
        }
    }

    /** Archiving, unarchiving and resuming move sessions in or out of the archived list. */
    private fun reloadArchivedIfLoaded() {
        if (archivedLoaded) reloadArchived(keepLoaded = true)
    }

    private fun updateArchivedRows() {
        val open = shownSessionIds().toSet()
        archivedSessions = archivedPager.sessions.orEmpty().map { row ->
            SessionListItemUiState(
                session = row.toUiModel(),
                relativeUpdatedAt = relativeTime(row.updatedAt),
                selected = row.id in open,
            )
        }
    }

    /** Saves a Gateway address and token typed in Settings; returns a message when they are unusable. */
    fun saveManualConnection(gatewayUrl: String, token: String): String? {
        val manual = validateManualConnection(gatewayUrl, token).getOrElse { return it.message }
        val saved = runCatching { connectionSource.saveManual(manual) }
            .getOrElse { return "Could not save the connection settings." }
        applyConnectionChange(saved)
        return null
    }

    fun clearManualConnection() {
        val restored = runCatching { connectionSource.clearManual() }
            .getOrElse { return reportError("Could not remove the saved connection settings.") }
        applyConnectionChange(restored)
    }

    private fun applyConnectionChange(next: DesktopGatewayConnection) {
        if (closed) return
        gatewayStartJob?.cancel()
        connection = next
        listError = next.configIssue
        connectionState = if (next.configIssue == null) DesktopConnectionState.CONNECTING else DesktopConnectionState.OFFLINE
        refreshSessions(userInitiated = true)
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
                val open = shownSessionIds().toSet()
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
                panesToClose(
                    openSessionIds = shownSessionIds(),
                    listedIds = rows.mapTo(HashSet()) { it.id },
                    archivedIds = archivedSessionIds(),
                ).forEach(::closeSession)
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
        if (isDetached(id)) {
            raiseWindow(id)
            return
        }
        val current = openSessionIdsState
        val next = openSessionInFocusedPane(current, selectedSessionId, id)
        current.firstOrNull { it !in next }?.let(::removeDetail)
        setOpenSessionIds(next)
        selection.select(id)
        updateSessionRowSelection()
        ensureDetail(id)
        saveLayout()
        markSeen(id)
    }

    /** Adds a session to the Desktop split view, or focuses it if it is already open. */
    fun openSessionInSplit(id: String) {
        if (closed) return
        if (isDetached(id)) {
            raiseWindow(id)
            return
        }
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
        markSeen(id)
    }

    fun focusSession(id: String) {
        if (id !in openSessionIdsState) return
        if (selection.selectedId == id) return
        selection.select(id)
        updateSessionRowSelection()
        saveLayout()
        markSeen(id)
    }

    /**
     * Read state lives in Herdr, where marking a session seen focuses its
     * pane. Only a session the user opens or selects is marked, never one that
     * merely stays open, so the Herdr view does not move behind their back.
     */
    private fun markSeen(id: String) {
        if (sessions.none { it.session.id == id && it.session.unread }) return
        sessions = sessions.map { row ->
            if (row.session.id == id) row.copy(session = row.session.copy(unread = false)) else row
        }
        scope.launch {
            try {
                repository.markSeen(id)
            } catch (cancel: CancellationException) {
                throw cancel
            } catch (error: Exception) {
                // The next list refresh brings the unread state back.
                logger.warning("mark seen failed: ${error.toDesktopGatewayError().kind}")
            }
        }
    }

    /** Closes a session's pane or, for a detached session, its window. */
    fun closeSession(id: String) {
        if (isDetached(id)) {
            detachedWindowsState = detachedWindowsState.filterNot { it.sessionId == id }
            removeDetail(id)
            updateSessionRowSelection()
            saveLayout()
            return
        }
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

    /**
     * Shows a session in its own window: a pane moves out of the main window
     * with its conversation and draft attachments, and a session that is not
     * open yet starts loading. The bounds are in screen points.
     */
    fun openSessionInWindow(
        id: String,
        x: Int? = null,
        y: Int? = null,
        width: Int = DEFAULT_DETACHED_WIDTH,
        height: Int = DEFAULT_DETACHED_HEIGHT,
    ) {
        if (closed) return
        if (isDetached(id)) {
            raiseWindow(id)
            return
        }
        // Shown in the window before it leaves the panes, so its detail loop never sees it closed.
        detachedWindowsState = detachedWindowsState +
            DesktopDetachedWindow(sessionId = id, x = x, y = y, width = width, height = height).normalized()
        val index = openSessionIdsState.indexOf(id)
        if (index >= 0) {
            val wasSelected = selection.selectedId == id
            setOpenSessionIds(openSessionIdsState - id)
            if (wasSelected) openSessionIdsState.lastOrNull()?.let(selection::select) ?: selection.clear()
        }
        updateSessionRowSelection()
        ensureDetail(id)
        saveLayout()
        if (index < 0) markSeen(id)
    }

    /** Moves a detached session back into the main window as a split pane at [index], closing its window. */
    fun returnToMainWindow(id: String, index: Int = openSessionIdsState.size) {
        if (closed || !isDetached(id)) return
        setOpenSessionIds(openSessionIdsState.toMutableList().apply { add(index.coerceIn(0, size), id) })
        detachedWindowsState = detachedWindowsState.filterNot { it.sessionId == id }
        selection.select(id)
        updateSessionRowSelection()
        ensureDetail(id)
        saveLayout()
    }

    /** Records where a detached window now sits; saved with the layout. */
    fun updateDetachedBounds(id: String, x: Int, y: Int, width: Int, height: Int) {
        val current = detachedWindowsState.firstOrNull { it.sessionId == id } ?: return
        val next = current.copy(x = x, y = y, width = width, height = height)
        if (next == current) return
        detachedWindowsState = detachedWindowsState.map { if (it.sessionId == id) next else it }
        saveLayout()
    }

    private fun raiseWindow(id: String) {
        windowRaiseRequest = id to ((windowRaiseRequest?.second ?: 0) + 1)
        markSeen(id)
    }

    private fun isDetached(id: String): Boolean = detachedWindowsState.any { it.sessionId == id }

    /** Whether the session is on screen anywhere, so its conversation keeps loading. */
    private fun isShown(id: String): Boolean = id in openSessionIdsState || isDetached(id)

    private fun shownSessionIds(): List<String> = openSessionIdsState + detachedWindowsState.map { it.sessionId }

    /**
     * Moves a pane to another slot, taking its width along. Call [saveLayout]
     * when the drag ends.
     */
    fun movePane(from: Int, to: Int) {
        val ids = openSessionIdsState
        if (from !in ids.indices || to !in ids.indices || from == to) return
        openSessionIdsState = ids.moved(from, to)
        if (paneWeights.size == ids.size) paneWeights = paneWeights.moved(from, to)
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
                detachedWindows = detachedWindowsState,
            ),
        )
    }

    /**
     * Reopens the panes from the previous launch. Call it after the first
     * composition: state written while this object is created inside
     * `remember` is not yet visible to the detail loops, which then stop.
     */
    fun restoreLayout(layout: DesktopLayoutSnapshot, screens: List<Rectangle> = emptyList()) {
        if (closed || shownSessionIds().isNotEmpty()) return
        val restored = layout.normalized()
        if (restored.openSessionIds.isEmpty() && restored.detachedWindows.isEmpty()) return
        openSessionIdsState = restored.openSessionIds
        paneWeights = restored.paneWeights
        detachedWindowsState = restored.detachedWindows.map { it.onScreen(screens) }
        restored.focusedSessionId?.let(selection::select)
        shownSessionIds().forEach(::ensureDetail)
        updateSessionRowSelection()
    }

    private fun setOpenSessionIds(ids: List<String>) {
        if (ids.size != openSessionIdsState.size) paneWeights = equalPaneWeights(ids.size)
        openSessionIdsState = ids
    }

    private fun ensureDetail(id: String) {
        if (detailJobs[id]?.isActive == true) return
        detailsBySession = detailsBySession + (id to loadingDetail(id))
        reloadSignals[id] = Channel(Channel.CONFLATED)
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
        reloadSignals.remove(id)
        detailsBySession = detailsBySession - id
        cleanupAttachments(composerAttachmentsBySession[id].orEmpty())
        composerAttachmentsBySession = composerAttachmentsBySession - id
    }

    private fun updateSessionRowSelection() {
        val open = shownSessionIds().toSet()
        sessions = sessions.map { it.copy(selected = it.session.id in open) }
        archivedSessions = archivedSessions.map { it.copy(selected = it.session.id in open) }
    }

    /** Archived sessions are not in the session list, but their panes stay open. */
    private fun archivedSessionIds(): Set<String> =
        archivedPager.sessions.orEmpty().mapTo(HashSet()) { it.id } +
            detailsBySession.values.mapNotNull { detail -> detail.session?.takeIf { it.archived }?.id }

    private suspend fun detailLoop(id: String) {
        var first = true
        var messages = emptyList<Message>()
        var anchor: String? = null
        // The loop owns the merged messages, so a reload runs in it too.
        val reloads = reloadSignals[id] ?: return
        var reload = false
        while (currentCoroutineContext().isActive && !closed && isShown(id)) {
            try {
                // Keep the explicit GET /sessions/{id} call on the first load;
                // it gives a useful Session-not-found error before messages.
                if (first) repository.session(id)
                val since = anchor.takeUnless { first || reload }
                var notice: ReloadNotice? = detailsBySession[id]?.reloadNotice
                var snapshot: MessageSnapshot? = null
                val response = if (reload) {
                    val r = repository.reload(id, pendingInteractionIds(messages))
                    notice = reloadNotice(r.findings)
                    MessagesResponse(r.session, r.messages)
                } else if (since == null) {
                    repository.messageSnapshot(id)
                } else {
                    repository.changedMessageSnapshot(id, since).also { snapshot = it }?.response
                }
                if (response == null) {
                    // Unchanged; only clear an error left by an earlier poll.
                    if (detailsBySession[id]?.error != null) updateDetail(id) { it.copy(error = null) }
                } else {
                    messages = mergeMessages(messages, response.messages.map(MessageDto::toMessage), since)
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
                        // A reload asked for during this poll is still to come.
                        reloading = !reload && detailsBySession[id]?.reloading == true,
                        reloadNotice = notice,
                        attachmentsEnabled = true,
                        showBackButton = false,
                        sendOnEnter = true,
                        sendWithModifier = false,
                    ))
                    snapshot?.acknowledge()
                    if (reload && notice != null) clearReloadNoticeLater(id, notice)
                    first = false
                    logger.fine("session detail refreshed")
                }
            } catch (cancel: CancellationException) {
                throw cancel
            } catch (error: Exception) {
                val mapped = error.toDesktopGatewayError(streaming = !first)
                if (isShown(id)) {
                    val current = detailsBySession[id] ?: loadingDetail(id)
                    updateDetail(id, (current).copy(
                        sessionId = id,
                        messages = messages,
                        pending = pendingBySession[id].orEmpty(),
                        attachments = composerAttachmentStates(id),
                        error = if (reload) "Reload failed: ${mapped.message}" else mapped.message,
                        attachmentsEnabled = true,
                        loading = first,
                        reloading = !reload && current.reloading,
                    ))
                }
                logger.warning("session detail failed: ${mapped.kind}")
            }
            reload = withTimeoutOrNull(DETAIL_POLL_MS) { reloads.receive() } != null
        }
    }

    /**
     * Reads the session again past every cache and asks the gateway whether
     * a question was missed; the detail loop picks it up at once.
     */
    fun reload(id: String) {
        if (detailsBySession[id]?.reloading != false) return
        updateDetail(id) { it.copy(reloading = true) }
        reloadSignals[id]?.trySend(Unit)
    }

    fun dismissReloadNotice(id: String) {
        updateDetail(id) { it.copy(reloadNotice = null) }
    }

    /** A clean reload is reported briefly; a missed question stays until dismissed. */
    private fun clearReloadNoticeLater(id: String, notice: ReloadNotice) {
        if (notice.missed) return
        scope.launch {
            delay(RELOAD_NOTICE_MS)
            updateDetail(id) { if (it.reloadNotice === notice) it.copy(reloadNotice = null) else it }
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
        if (!isShown(sessionId)) {
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
        if (closed || !isShown(id)) return
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
        val delivery = DesktopPendingDelivery(MessageDeliveryAttempt(pending.localId, api), text, attachments.toList())
        pendingDeliveries[pending.localId] = delivery
        pendingMessageBaselines = pendingMessageBaselines + (
            pending.localId to detailsBySession[id]?.messages.orEmpty().map(Message::id).toSet()
        )
        updatePending(id) { it + pending }
        composerAttachmentsBySession = composerAttachmentsBySession - id
        updateDetail(id) { it.copy(attachments = emptyList()) }
        deliverPending(id, pending.localId, delivery)
    }

    fun retry(localId: String) {
        selectedSessionId?.let { retry(it, localId) }
    }

    fun retry(id: String, localId: String) {
        if (closed) return
        val original = pendingBySession[id].orEmpty().firstOrNull { it.localId == localId } ?: return
        val delivery = pendingDeliveries[localId] ?: return
        if (original.state != SendState.FAILED || !original.canRetry || !delivery.attempt.canRetry) return
        deliverPending(id, localId, delivery)
    }

    private fun deliverPending(id: String, localId: String, delivery: DesktopPendingDelivery) {
        // Acquire before launching so even simultaneous retry callbacks cannot
        // queue a second replay while the first is waiting on the network.
        if (!delivery.lock.tryLock()) return
        updatePending(id) { rows -> rows.map { if (it.localId == localId) it.copy(state = SendState.SENDING, error = null) else it } }
        val sessionLock = deliverySessionLocks.getOrPut(id) { Mutex() }
        // Enter the session queue on the caller's thread, preserving enqueue
        // order before the Default dispatcher can schedule another message.
        scope.launch(start = CoroutineStart.UNDISPATCHED) {
            var released = false
            try {
                sessionLock.withLock {
                    var failure: Exception? = null
                    try {
                        delivery.attempt.send(api, id, delivery.text) { pinnedApi ->
                            uploadDesktopAttachments(pinnedApi, delivery.attachments)
                        }
                    } catch (cancel: CancellationException) {
                        throw cancel
                    } catch (error: Exception) {
                        failure = error
                    }
                    // FAILED is an invitation to retry. Make the attempt
                    // available before publishing it, including when logging
                    // or another message's completion takes time.
                    released = true
                    delivery.lock.unlock()
                    val error = failure
                    if (error == null) {
                        updatePending(id) { rows -> rows.map { if (it.localId == localId) it.copy(state = SendState.ACCEPTED) else it } }
                        logger.fine("message accepted by Gateway")
                    } else {
                        val mapped = error.toDesktopGatewayError()
                        val description = if (!delivery.attempt.canRetry || error is UnconfirmedMessageDeliveryException) error.message else mapped.message
                        updatePending(id) { rows -> rows.map {
                            if (it.localId == localId) it.copy(
                                state = SendState.FAILED,
                                error = description,
                                canRetry = delivery.attempt.canRetry,
                            ) else it
                        } }
                        logger.warning("message send failed: ${mapped.kind}")
                    }
                }
            } finally {
                if (!released) delivery.lock.unlock()
            }
        }
    }

    fun discard(localId: String) {
        selectedSessionId?.let { discard(it, localId) }
    }

    fun discard(id: String, localId: String) {
        cleanupAttachments(pendingAttachments[localId].orEmpty())
        pendingAttachments = pendingAttachments - localId
        pendingDeliveries.remove(localId)
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
                if (isShown(id)) updateDetail(id) { it.copy(answering = false) }
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

    fun requestArchive(ref: SessionRef) = requestArchive(listOf(ref))

    /**
     * Archives [refs] in one gateway request. Archiving a running session
     * kills its agent, so a selection with one asks first.
     */
    fun requestArchive(refs: List<SessionRef>) {
        val models = refs.distinctBy { it.id }.filter { it.id !in busySessionIds }.mapNotNull { ref ->
            sessions.firstOrNull { it.session.id == ref.id }?.session
                ?: detail?.session?.takeIf { it.id == ref.id }
                ?: detailFor(ref.id)?.session
        }
        when {
            models.isEmpty() -> if (refs.any { it.id !in busySessionIds }) reportError("Session not found.")
            models.any { it.live } -> archiveConfirmation = models
            else -> archiveNow(models.map { it.id })
        }
    }

    fun confirmArchive() {
        val ids = archiveConfirmation.map { it.id }
        archiveConfirmation = emptyList()
        if (ids.isNotEmpty()) archiveNow(ids)
    }

    fun cancelArchive() {
        archiveConfirmation = emptyList()
    }

    private fun archiveNow(ids: List<String>) {
        ids.forEach { setBusy(it, true) }
        scope.launch {
            try {
                val archived = api.archive(ids)
                archived.mapNotNull { it.warning }.takeIf { it.isNotEmpty() }?.let { notice = it.joinToString("\n") }
                ids.filter(::isShown).forEach(::closeSession)
                refreshSessions()
                reloadArchivedIfLoaded()
                logger.info("sessions archived: ${ids.size}")
            } catch (error: Exception) {
                val mapped = error.toDesktopGatewayError()
                reportError(mapped.message)
                logger.warning("session archive failed: ${mapped.kind}")
                // The gateway may have archived some before failing.
                refreshSessions()
                reloadArchivedIfLoaded()
            } finally {
                ids.forEach { setBusy(it, false) }
            }
        }
    }

    fun unarchive(ref: SessionRef) = unarchive(listOf(ref))

    /** Restores [refs] one at a time; the gateway has no batch endpoint for it. */
    fun unarchive(refs: List<SessionRef>) {
        val ids = refs.map { it.id }.distinct().filter { it !in busySessionIds }
        if (ids.isEmpty()) return
        ids.forEach { setBusy(it, true) }
        scope.launch {
            var failures = 0
            var lastError: DesktopGatewayError? = null
            for (id in ids) {
                try {
                    api.unarchive(id)
                } catch (error: Exception) {
                    val mapped = error.toDesktopGatewayError()
                    failures++
                    lastError = mapped
                    logger.warning("session unarchive failed: ${mapped.kind}")
                } finally {
                    setBusy(id, false)
                }
            }
            lastError?.let {
                reportError(if (ids.size == 1) it.message else "Could not restore $failures of ${ids.size} sessions: ${it.message}")
            }
            refreshSessions()
            reloadArchivedIfLoaded()
        }
    }

    fun resume(ref: SessionRef) {
        setBusy(ref.id, true)
        scope.launch {
            try {
                api.resume(ref.id, trust = true)
                refreshSessions()
                reloadArchivedIfLoaded()
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

    fun dismissNotice() {
        notice = null
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
        if (isShown(id)) detailsBySession = detailsBySession + (id to value)
    }

    private fun updateDetail(id: String, update: (SessionDetailUiState) -> SessionDetailUiState) {
        detailsBySession[id]?.let { updateDetail(id, update(it)) }
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
            settledIds.forEach(pendingDeliveries::remove)
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
        pendingDeliveries.clear()
        listJob?.cancel()
        gatewayStartJob?.cancel()
        scope.cancel()
        http.connectionPool.evictAll()
        http.dispatcher.executorService.shutdown()
    }
}

private class DesktopPendingDelivery(
    val attempt: MessageDeliveryAttempt,
    val text: String,
    val attachments: List<DesktopAttachment>,
) {
    val lock = Mutex()
}

/** Uploads the files to the gateway and returns their upload ids. */
internal suspend fun uploadDesktopAttachments(api: GatewayApi, attachments: List<DesktopAttachment>): List<String> =
    attachments.map { attachment ->
        val size = withContext(kotlinx.coroutines.Dispatchers.IO) { Files.size(attachment.path) }
        api.upload(attachment.mimeType, attachment.name, size) {
            Files.newInputStream(attachment.path)
        }
    }

internal fun cleanupAttachments(attachments: Iterable<DesktopAttachment>) {
    attachments.filter { it.deleteWhenDone }.forEach { attachment ->
        runCatching { Files.deleteIfExists(attachment.path) }
    }
}

/**
 * Panes whose session left the list, e.g. one deleted elsewhere. An archived
 * session is not listed but stays readable, so its pane is kept.
 */
internal fun panesToClose(
    openSessionIds: List<String>,
    listedIds: Set<String>,
    archivedIds: Set<String>,
): List<String> = openSessionIds.filter { it !in listedIds && it !in archivedIds }

internal fun <T> List<T>.moved(from: Int, to: Int): List<T> =
    toMutableList().apply { add(to, removeAt(from)) }

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

internal fun DesktopAttachment.toUiState() = AttachmentUiState(
    id = id,
    name = name,
    mimeType = mimeType,
)

private fun normalize(value: String): String = value.replace(Regex("\\s+"), " ").trim()

/**
 * Applies a messages response. An incremental response starts at [anchor] and
 * replaces the tail from it; anything else is the whole conversation. Rows the
 * gateway no longer returns, like a queued prompt the agent has taken in, are
 * dropped rather than kept beside their replacement.
 */
internal fun mergeMessages(old: List<Message>, incoming: List<Message>, anchor: String?): List<Message> {
    val at = anchor?.let { id -> old.indexOfLast { it.id == id } } ?: -1
    if (at < 0 || incoming.firstOrNull()?.id != anchor) return incoming
    return old.take(at) + incoming
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
