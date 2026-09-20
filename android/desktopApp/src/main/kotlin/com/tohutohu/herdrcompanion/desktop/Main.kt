package com.tohutohu.herdrcompanion.desktop

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.gestures.Orientation
import androidx.compose.foundation.gestures.draggable
import androidx.compose.foundation.gestures.rememberDraggableState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Clear
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Switch
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.VerticalDivider
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.KeyShortcut
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.input.pointer.PointerIcon
import androidx.compose.ui.input.pointer.pointerHoverIcon
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.FrameWindowScope
import androidx.compose.ui.window.MenuBar
import androidx.compose.ui.window.Window
import androidx.compose.ui.window.WindowState
import androidx.compose.ui.window.application
import androidx.compose.ui.window.rememberWindowState
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import com.tohutohu.herdrcompanion.model.SessionUiModel
import com.tohutohu.herdrcompanion.model.headline
import com.tohutohu.herdrcompanion.ui.detail.SessionDetailAction
import com.tohutohu.herdrcompanion.ui.detail.SessionDetailScreen
import com.tohutohu.herdrcompanion.ui.sessions.SessionListAction
import com.tohutohu.herdrcompanion.ui.sessions.SessionListScreen
import com.tohutohu.herdrcompanion.ui.sessions.SessionListUiState
import com.tohutohu.herdrcompanion.ui.sessions.SessionRef
import com.tohutohu.herdrcompanion.ui.theme.SharedTheme
import java.awt.Cursor
import java.awt.Frame
import java.awt.event.ComponentAdapter
import java.awt.event.ComponentEvent
import java.awt.event.WindowEvent
import java.awt.event.WindowStateListener

private val MIN_SIDEBAR_WIDTH = 240.dp
private val MIN_DETAIL_WIDTH = 420.dp
private val MIN_SPLIT_DETAIL_WIDTH = 300.dp

fun main() {
    val instanceLock = DesktopInstanceLock.tryAcquire()
    if (instanceLock == null) {
        DesktopInstanceLock.activateExisting()
        return
    }
    try {
        application {
            val restored = remember { DesktopPreferences.loadWindow() }
            val windowState = rememberWindowState(
                placement = restored.placement(),
                position = restored.position(),
                width = restored.width.dp,
                height = restored.height.dp,
            )
            Window(
                onCloseRequest = ::exitApplication,
                title = "Herdr Companion",
                state = windowState,
            ) {
                window.minimumSize = java.awt.Dimension(880, 560)
                val connectionSource = remember { DesktopConnectionSource() }
                val http = remember { DesktopHttp.client() }
                val api = remember { GatewayApi(http) { connectionSource.current.settings } }
                val appState = remember { DesktopAppState(connectionSource, api, http) }
                DesktopWindowPersistence(window)
                DisposableEffect(appState) {
                    onDispose { appState.close() }
                }
                SharedTheme {
                    DesktopShell(appState, api, onCloseWindow = ::exitApplication)
                }
            }
        }
    } finally {
        instanceLock.close()
    }
}

@Composable
private fun DesktopWindowPersistence(window: java.awt.Window) {
    DisposableEffect(window) {
        val componentListener = object : ComponentAdapter() {
            override fun componentMoved(event: ComponentEvent) = DesktopPreferences.saveWindow(window)
            override fun componentResized(event: ComponentEvent) = DesktopPreferences.saveWindow(window)
        }
        val stateListener = WindowStateListener { _: WindowEvent -> DesktopPreferences.saveWindow(window) }
        window.addComponentListener(componentListener)
        (window as? Frame)?.addWindowStateListener(stateListener)
        onDispose {
            DesktopPreferences.saveWindow(window)
            window.removeComponentListener(componentListener)
            (window as? Frame)?.removeWindowStateListener(stateListener)
        }
    }
}

@OptIn(ExperimentalFoundationApi::class, ExperimentalComposeUiApi::class)
@Composable
private fun FrameWindowScope.DesktopShell(
    state: DesktopAppState,
    api: GatewayApi,
    onCloseWindow: () -> Unit,
) {
    var query by rememberSaveable { mutableStateOf("") }
    var highlightedIndex by rememberSaveable { mutableStateOf(0) }
    var searchFocused by remember { mutableStateOf(false) }
    var settingsOpen by remember { mutableStateOf(false) }
    var aboutOpen by remember { mutableStateOf(false) }
    val searchFocusRequester = remember { FocusRequester() }
    val sessions = state.sessions.filterDesktopSessions(query)
    val highlightedId = if (searchFocused) sessions.getOrNull(highlightedIndex)?.session?.id else null

    LaunchedEffect(sessions.size, query) {
        highlightedIndex = highlightedIndex.coerceIn(0, (sessions.size - 1).coerceAtLeast(0))
    }

    fun focusSearch() {
        searchFocused = true
        searchFocusRequester.requestFocus()
    }

    val actions = DesktopActions(
        state = state,
        focusSearch = ::focusSearch,
        openSettings = { settingsOpen = true },
        openAbout = { aboutOpen = true },
        closeWindow = onCloseWindow,
    )

    DesktopMenuBar(actions)

    Surface(
        modifier = Modifier.fillMaxSize(),
        color = MaterialTheme.colorScheme.background,
        contentColor = MaterialTheme.colorScheme.onBackground,
    ) {
        Column(Modifier.fillMaxSize()) {
            Row(
            Modifier.fillMaxWidth().padding(horizontal = 18.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            Text("Herdr Companion", style = MaterialTheme.typography.headlineSmall)
            ConnectionIndicator(state.connectionState, state.connectionLabel)
            Spacer(Modifier.weight(1f))
            OutlinedTextField(
                value = query,
                onValueChange = {
                    query = it
                    highlightedIndex = 0
                },
                modifier = Modifier
                    .widthIn(min = 240.dp, max = 420.dp)
                    .focusRequester(searchFocusRequester)
                    .onFocusChanged { searchFocused = it.isFocused }
                    .onPreviewKeyEvent { event ->
                        if (event.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                        when (event.key) {
                            Key.DirectionDown -> {
                                highlightedIndex = nextDesktopSearchIndex(highlightedIndex, sessions.size, 1)
                                true
                            }
                            Key.DirectionUp -> {
                                highlightedIndex = nextDesktopSearchIndex(highlightedIndex, sessions.size, -1)
                                true
                            }
                            Key.Enter -> {
                                sessions.getOrNull(highlightedIndex)?.session?.id?.let {
                                    actions.openSession(it)
                                    searchFocused = false
                                }
                                true
                            }
                            Key.Escape -> {
                                query = ""
                                highlightedIndex = 0
                                searchFocused = false
                                true
                            }
                            else -> false
                        }
                    },
                placeholder = { Text("Search sessions…") },
                leadingIcon = { Icon(Icons.Default.Search, contentDescription = null) },
                trailingIcon = {
                    if (query.isNotEmpty()) {
                        IconButton(onClick = { query = ""; highlightedIndex = 0 }) {
                            Icon(Icons.Default.Clear, contentDescription = "Clear search")
                        }
                    }
                },
                singleLine = true,
            )
            TextButton(onClick = actions::refresh) {
                Icon(Icons.Default.Refresh, contentDescription = null)
                Text("Refresh", modifier = Modifier.padding(start = 4.dp))
            }
            Button(onClick = actions::newSession) { Text("+ New Session") }
        }
            state.transientError?.let { message ->
            Row(
                Modifier.fillMaxWidth().padding(horizontal = 18.dp, vertical = 2.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(message, color = MaterialTheme.colorScheme.error, modifier = Modifier.weight(1f))
                TextButton(onClick = { state.dismissError(); actions.refresh() }) { Text("Retry") }
            }
        }
            HorizontalDivider()
            BoxWithConstraints(Modifier.fillMaxSize()) {
            var sidebarWidth by remember { mutableStateOf(DesktopPreferences.loadSidebarWidth().dp) }
            val detailPaneCount = state.openSessionIds.size.coerceAtLeast(1)
            val minDetailWidth = if (detailPaneCount > 1) {
                MIN_SPLIT_DETAIL_WIDTH * detailPaneCount + 1.dp
            } else {
                MIN_DETAIL_WIDTH
            }
            val maxSidebarWidth = (maxWidth * 0.4f)
                .coerceAtMost((maxWidth - minDetailWidth - 8.dp).coerceAtLeast(MIN_SIDEBAR_WIDTH))
                .coerceAtLeast(MIN_SIDEBAR_WIDTH)
            val safeSidebarWidth = sidebarWidth.coerceIn(MIN_SIDEBAR_WIDTH, maxSidebarWidth)
            LaunchedEffect(maxSidebarWidth) {
                if (sidebarWidth != safeSidebarWidth) sidebarWidth = safeSidebarWidth
                DesktopPreferences.saveSidebarWidth(safeSidebarWidth.value)
            }
            val density = androidx.compose.ui.platform.LocalDensity.current
            val dragState = rememberDraggableState { deltaPx ->
                sidebarWidth = (sidebarWidth.value + deltaPx / density.density)
                    .dp.coerceIn(MIN_SIDEBAR_WIDTH, maxSidebarWidth)
            }

                Row(Modifier.fillMaxSize()) {
                Column(Modifier.width(safeSidebarWidth).fillMaxHeight()) {
                    Row(
                        Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 10.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Text("Sessions", style = MaterialTheme.typography.titleMedium)
                        Spacer(Modifier.weight(1f))
                        if (state.isSplitView) {
                            TextButton(onClick = state::collapseSplitView) { Text("Single view") }
                        }
                        if (query.isNotBlank()) {
                            Text(
                                "${sessions.size}/${state.sessions.size}",
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                    if (query.isNotBlank() && state.openSessionIds.any { it !in sessions.map { item -> item.session.id } }) {
                        Text(
                            "Open panes stay visible while filtered out",
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(horizontal = 16.dp, vertical = 2.dp),
                        )
                    }
                    SessionListScreen(
                        state = SessionListUiState(
                            sessions = sessions.map { item ->
                                item.copy(
                                    selected = item.selected ||
                                        item.session.id in state.openSessionIds ||
                                        item.session.id == highlightedId,
                                )
                            },
                            isLoaded = state.listLoaded,
                            isRefreshing = state.listRefreshing,
                            error = state.listError,
                            emptyMessage = if (query.isBlank()) {
                                "No sessions. Start Claude Code or Codex inside Herdr on your Mac."
                            } else {
                                "No sessions match \"$query\"."
                            },
                            busySessionIds = state.busySessionIds,
                            engagedSessionIds = state.busySessionIds +
                                (state.archiveConfirmation?.id?.let { setOf(it) } ?: emptySet()),
                        ),
                        showTopBar = false,
                        showNewSessionFab = false,
                        sessionContextMenu = { item, content ->
                            DesktopSessionContextMenu(
                                item = item,
                                onOpen = { state.selectSession(item.session.id) },
                                onOpenInSplit = { state.openSessionInSplit(item.session.id) },
                                onArchive = {
                                    if (item.session.archived) {
                                        handleListAction(state, SessionListAction.Unarchive(listOf(item.session.toRef())))
                                    } else {
                                        handleListAction(state, SessionListAction.Archive(listOf(item.session.toRef())))
                                    }
                                },
                                onRefresh = actions::refresh,
                                onCopyId = { actions.copySessionId(item.session.id) },
                                onOpenDirectory = {
                                    val path = item.session.cwd ?: item.session.project
                                    if (path.isBlank() || !DesktopPlatformActions.openPath(path)) {
                                        state.reportError("Project directory is not available on this Mac.")
                                    }
                                },
                                content = content,
                            )
                        },
                        onAction = { action -> handleListAction(state, action) },
                    )
                }
                VerticalDivider(
                    modifier = Modifier
                        .width(8.dp)
                        .fillMaxHeight()
                        .draggable(
                            orientation = Orientation.Horizontal,
                            state = dragState,
                            onDragStopped = { DesktopPreferences.saveSidebarWidth(safeSidebarWidth.value) },
                        )
                        .combinedClickable(
                            onClick = {},
                            onDoubleClick = {
                                sidebarWidth = 320.dp
                                DesktopPreferences.saveSidebarWidth(320f)
                            },
                        )
                        .pointerHoverIcon(PointerIcon(Cursor.getPredefinedCursor(Cursor.E_RESIZE_CURSOR))),
                )
                Box(Modifier.weight(1f).fillMaxHeight().widthIn(min = minDetailWidth)) {
                    val detailIds = state.openSessionIds
                    if (detailIds.isEmpty()) {
                        EmptyDetail(state)
                    } else {
                        Row(Modifier.fillMaxSize()) {
                            detailIds.forEachIndexed { index, id ->
                                if (index > 0) {
                                    VerticalDivider(Modifier.fillMaxHeight().width(1.dp))
                                }
                                key(id) {
                                    Box(
                                        Modifier
                                            .weight(1f)
                                            .fillMaxHeight()
                                            .widthIn(min = if (detailIds.size > 1) MIN_SPLIT_DETAIL_WIDTH else MIN_DETAIL_WIDTH),
                                    ) {
                                        DesktopSessionPane(
                                            state = state,
                                            api = api,
                                            window = window,
                                            sessionId = id,
                                            paneNumber = index + 1,
                                            paneCount = detailIds.size,
                                        )
                                    }
                                }
                            }
                        }
                    }
                }
                }
            }
        }
    }

    LaunchedEffect(state.notificationEvent?.id) {
        val event = state.notificationEvent ?: return@LaunchedEffect
        if (!window.isActive && DesktopPreferences.notificationsEnabled) {
            DesktopNotificationService.show(event)
        }
        state.dismissNotification(event.id)
    }

    if (state.newSessionOpen) {
        DesktopNewSessionWindow(
            api = api,
            onCreated = { id ->
                state.closeNewSession()
                actions.refresh()
                if (id != null) state.selectSession(id)
            },
            onDismiss = state::closeNewSession,
            onError = state::reportError,
        )
    }

    state.archiveConfirmation?.let {
        AlertDialog(
            onDismissRequest = state::cancelArchive,
            title = { Text("Stop and archive this session?") },
            text = { Text("The running agent pane will be closed. You can resume the session later.") },
            confirmButton = { TextButton(onClick = state::confirmArchive) { Text("Stop and archive") } },
            dismissButton = { TextButton(onClick = state::cancelArchive) { Text("Cancel") } },
        )
    }

    if (settingsOpen) DesktopSettingsDialog(state, onDismiss = { settingsOpen = false })
    if (aboutOpen) {
        AlertDialog(
            onDismissRequest = { aboutOpen = false },
            title = { Text("About Herdr Companion") },
            text = { Text("Herdr Companion\nCompose Multiplatform client for the Herdr Companion Gateway.") },
            confirmButton = { TextButton(onClick = { aboutOpen = false }) { Text("OK") } },
        )
    }
}

@Composable
private fun DesktopSessionPane(
    state: DesktopAppState,
    api: GatewayApi,
    window: java.awt.Window,
    sessionId: String,
    paneNumber: Int,
    paneCount: Int,
) {
    val detail = state.detailFor(sessionId)
    val dropTarget = rememberDesktopAttachmentDropTarget(
        enabled = detail?.session?.canSend == true,
        onFilesDropped = { paths -> state.addAttachments(sessionId, paths) },
    )
    Column(Modifier.fillMaxSize()) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .background(
                    if (state.selectedSessionId == sessionId) {
                        MaterialTheme.colorScheme.secondaryContainer
                    } else {
                        MaterialTheme.colorScheme.surfaceVariant
                    },
                )
                .clickable { state.focusSession(sessionId) }
                .padding(start = 12.dp, end = 4.dp, top = 2.dp, bottom = 2.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = "Pane $paneNumber of $paneCount",
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Text(
                text = detail?.session?.headline() ?: sessionId,
                style = MaterialTheme.typography.titleSmall,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f).padding(start = 8.dp),
            )
            IconButton(
                onClick = { state.closeSession(sessionId) },
                modifier = Modifier.size(32.dp),
            ) {
                Icon(Icons.Default.Close, contentDescription = "Close session pane")
            }
        }
        Box(Modifier.weight(1f).fillMaxWidth()) {
            if (detail == null) {
                Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
            } else {
                SessionDetailScreen(
                    state = detail,
                    resolveUrl = api::absolute,
                    formatFileSize = ::formatFileSize,
                    resolveAttachmentPreview = { id -> state.resolveAttachmentPreview(id) },
                    composerModifier = dropTarget.modifier,
                    attachmentDropActive = dropTarget.active,
                    onAction = { action -> handleDetailAction(state, api, window, sessionId, action) },
                )
            }
        }
    }
}

private class DesktopActions(
    private val state: DesktopAppState,
    private val focusSearch: () -> Unit,
    private val openSettings: () -> Unit,
    private val openAbout: () -> Unit,
    private val closeWindow: () -> Unit,
) {
    fun newSession() = state.openNewSession()
    fun refresh() = state.refreshSessions(userInitiated = true)
    fun openSearch() = focusSearch()
    fun settings() = openSettings()
    fun about() = openAbout()
    fun close() = closeWindow()
    fun openSession(id: String) = if (state.isSplitView) {
        state.openSessionInSplit(id)
    } else {
        state.selectSession(id)
    }
    fun singleView() = state.collapseSplitView()
    fun hasSplitView() = state.isSplitView
    fun archive() = state.detail?.session?.let { state.requestArchive(it.toRef()) }
    fun copySelectedSessionId() = copySessionId(state.selectedSessionId)
    fun copySessionId(id: String?) {
        if (id == null || !DesktopPlatformActions.copyText(id)) state.reportError("Nothing is selected to copy.")
    }
    fun hasSelectedSession() = state.selectedSessionId != null
    fun hasProjectDirectory() = state.detail?.session?.let { !it.cwd.isNullOrBlank() || it.project.isNotBlank() } == true
    fun openProjectDirectory() {
        val session = state.detail?.session ?: return
        val directory = session.cwd ?: session.project
        if (directory.isBlank() || !DesktopPlatformActions.openPath(directory)) {
            state.reportError("Project directory is not available on this Mac.")
        }
    }
}

@OptIn(ExperimentalComposeUiApi::class)
@Composable
private fun FrameWindowScope.DesktopMenuBar(actions: DesktopActions) {
    MenuBar {
        Menu("Herdr Companion", mnemonic = 'H') {
            Item("About Herdr Companion", onClick = actions::about)
            Item("Settings…", shortcut = KeyShortcut(Key.Comma, ctrl = true), onClick = actions::settings)
            Separator()
            Item("Close Window", shortcut = KeyShortcut(Key.W, ctrl = true), onClick = actions::close)
        }
        Menu("File", mnemonic = 'F') {
            Item("New Session", shortcut = KeyShortcut(Key.N, ctrl = true), onClick = actions::newSession)
        }
        Menu("View", mnemonic = 'V') {
            Item("Search Sessions", shortcut = KeyShortcut(Key.K, ctrl = true), onClick = actions::openSearch)
            Item("Refresh", shortcut = KeyShortcut(Key.R, ctrl = true), onClick = actions::refresh)
            Item("Single Session View", enabled = actions.hasSplitView(), onClick = actions::singleView)
        }
        Menu("Session", mnemonic = 'S') {
            Item("Archive", enabled = actions.hasSelectedSession(), onClick = actions::archive)
            Item("Copy Session ID", enabled = actions.hasSelectedSession(), onClick = actions::copySelectedSessionId)
            Item("Open Project Directory", enabled = actions.hasProjectDirectory(), onClick = actions::openProjectDirectory)
        }
    }
}

@Composable
private fun ConnectionIndicator(status: DesktopConnectionState, label: String) {
    val color = when (status) {
        DesktopConnectionState.CONNECTED -> Color(0xFF2E9D5B)
        DesktopConnectionState.CONNECTING,
        DesktopConnectionState.RECONNECTING -> MaterialTheme.colorScheme.tertiary
        DesktopConnectionState.OFFLINE,
        DesktopConnectionState.AUTHENTICATION_FAILED -> MaterialTheme.colorScheme.error
    }
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
        Box(Modifier.size(8.dp).background(color, CircleShape))
        Text(label, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
private fun DesktopSettingsDialog(state: DesktopAppState, onDismiss: () -> Unit) {
    var notifications by remember { mutableStateOf(DesktopPreferences.notificationsEnabled) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Settings") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Text("Gateway", style = MaterialTheme.typography.titleSmall)
                Text(state.connection.baseUrl, style = MaterialTheme.typography.bodySmall)
                Text(
                    state.connection.configPath?.toString()
                        ?: "Gateway config not found; start the existing macOS manager first.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text("Desktop notifications", modifier = Modifier.weight(1f))
                    Switch(checked = notifications, onCheckedChange = { notifications = it })
                }
                Text(
                    "Gateway host and port are owned by the macOS menu-bar manager.",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        },
        confirmButton = {
            TextButton(onClick = {
                DesktopPreferences.notificationsEnabled = notifications
                onDismiss()
            }) { Text("Done") }
        },
    )
}

@Composable
private fun EmptyDetail(state: DesktopAppState) {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        if (!state.listLoaded && state.listError == null) {
            Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(10.dp)) {
                CircularProgressIndicator()
                Text("Loading sessions…", style = MaterialTheme.typography.bodyMedium)
            }
        } else if (state.listError != null && state.sessions.isEmpty()) {
            Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text("Gateway connection needs attention", style = MaterialTheme.typography.titleMedium)
                Text(state.listError!!, color = MaterialTheme.colorScheme.error)
                TextButton(onClick = { state.refreshSessions(userInitiated = true) }) { Text("Retry") }
                if (state.connectionState == DesktopConnectionState.OFFLINE ||
                    state.connectionState == DesktopConnectionState.RECONNECTING
                ) {
                    Button(onClick = state::startGateway) { Text("Start Herdr Companion Gateway") }
                }
            }
        } else {
            Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text("Select a session", style = MaterialTheme.typography.titleLarge)
                Text("Choose a session from the sidebar to view its conversation.", color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
    }
}

private fun handleListAction(state: DesktopAppState, action: SessionListAction) {
    when (action) {
        is SessionListAction.OpenSession -> if (state.isSplitView) {
            state.openSessionInSplit(action.sessionId)
        } else {
            state.selectSession(action.sessionId)
        }
        is SessionListAction.Archive -> action.sessions.firstOrNull()?.let(state::requestArchive)
        is SessionListAction.Unarchive -> action.sessions.firstOrNull()?.let(state::unarchive)
        is SessionListAction.Resume -> state.resume(action.session)
        SessionListAction.OpenNewSession -> state.openNewSession()
        SessionListAction.Refresh -> state.refreshSessions(userInitiated = true)
        SessionListAction.OpenArchived -> state.reportError("Archived session browsing is not yet available on Desktop.")
        SessionListAction.OpenSettings -> state.reportError("Use the Herdr Companion menu's Settings item for Desktop connection status.")
        is SessionListAction.OpenStarting -> state.reportError("Session startup is handled in the New Session window.")
    }
}

private fun handleDetailAction(
    state: DesktopAppState,
    api: GatewayApi,
    window: java.awt.Window,
    sessionId: String,
    action: SessionDetailAction,
) {
    when (action) {
        SessionDetailAction.Back -> state.closeSession(sessionId)
        is SessionDetailAction.Send -> state.send(sessionId, action.text)
        is SessionDetailAction.Retry -> state.retry(sessionId, action.localId)
        is SessionDetailAction.Discard -> state.discard(sessionId, action.localId)
        is SessionDetailAction.Respond -> state.respond(sessionId, action.response)
        SessionDetailAction.Archive -> state.detailFor(sessionId)?.session?.let { state.requestArchive(it.toRef()) }
        SessionDetailAction.Unarchive -> state.detailFor(sessionId)?.session?.let { state.unarchive(it.toRef()) }
        SessionDetailAction.Resume -> state.detailFor(sessionId)?.session?.let { state.resume(it.toRef()) }
        SessionDetailAction.PickImage -> state.addAttachments(sessionId, DesktopFilePicker.pickFiles(window, imagesOnly = true))
        SessionDetailAction.PickFile -> state.addAttachments(sessionId, DesktopFilePicker.pickFiles(window))
        is SessionDetailAction.RemoveAttachment -> state.removeAttachment(sessionId, action.id)
        is SessionDetailAction.OpenFile -> {
            val cwd = state.detailFor(sessionId)?.session?.cwd
            if (!DesktopPlatformActions.openPath(action.path, cwd)) state.reportError("Could not open ${action.path} on this Mac.")
        }
        is SessionDetailAction.OpenImage -> {
            if (!DesktopPlatformActions.openUrl(api.absolute(action.url))) state.reportError("Could not open the image in the browser.")
        }
        SessionDetailAction.OpenTerminal -> {
            if (!DesktopPlatformActions.openTerminal(state.detailFor(sessionId)?.session?.cwd)) state.reportError("Could not open Terminal for this project.")
        }
    }
}

private fun SessionUiModel.toRef() = SessionRef(id, live, archived)

private fun formatFileSize(bytes: Long): String = when {
    bytes < 1024 -> "$bytes B"
    bytes < 1024 * 1024 -> "${bytes / 1024} KB"
    bytes < 1024 * 1024 * 1024 -> "${bytes / (1024 * 1024)} MB"
    else -> "${bytes / (1024 * 1024 * 1024)} GB"
}

private object DesktopHttp {
    fun client(): okhttp3.OkHttpClient = okhttp3.OkHttpClient.Builder()
        .connectTimeout(10, java.util.concurrent.TimeUnit.SECONDS)
        .readTimeout(30, java.util.concurrent.TimeUnit.SECONDS)
        .build()
}
