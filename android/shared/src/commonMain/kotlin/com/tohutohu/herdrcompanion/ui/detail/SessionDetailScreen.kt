package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.SizeTransform
import androidx.compose.animation.animateContentSize
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.DragInteraction
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Terminal
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.foundation.BorderStroke
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isMetaPressed
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.api.InteractionResponseDto
import com.tohutohu.herdrcompanion.data.api.Status
import com.tohutohu.herdrcompanion.model.headline
import com.tohutohu.herdrcompanion.ui.ContextBar
import com.tohutohu.herdrcompanion.ui.ContextGauge
import com.tohutohu.herdrcompanion.ui.ExpandingContent
import com.tohutohu.herdrcompanion.ui.SwapContent
import com.tohutohu.herdrcompanion.ui.SyncIndicator
import com.tohutohu.herdrcompanion.ui.agentSettingsLabel
import com.tohutohu.herdrcompanion.ui.changedInPlace
import com.tohutohu.herdrcompanion.ui.pausePlacementOnChange
import com.tohutohu.herdrcompanion.ui.costLabel
import com.tohutohu.herdrcompanion.ui.sessions.SessionActionMenuItems
import com.tohutohu.herdrcompanion.ui.sessions.toSessionRef
import com.tohutohu.herdrcompanion.ui.statusStyle
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * Pure Compose rendering for a session detail. The route supplies state,
 * gateway URL/file-size formatting, and handles every external side effect
 * through [SessionDetailAction].
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SessionDetailScreen(
    state: SessionDetailUiState,
    focusLatest: Boolean = false,
    resolveUrl: (String) -> String,
    formatFileSize: (Long) -> String,
    resolveAttachmentPreview: (String) -> Any? = { null },
    composerModifier: Modifier = Modifier,
    attachmentDropActive: Boolean = false,
    onPasteImage: (() -> Boolean)? = null,
    onAction: (SessionDetailAction) -> Unit,
) {
    val session = state.session
    val messages = state.messages
    val pending = state.pending
    val error = state.error
    val answering = state.answering
    var menu by remember { mutableStateOf(false) }
    // The header carries only what changes; the directory and the context bar
    // open under it when the title or the context ring is tapped.
    var detailsOpen by rememberSaveable(state.sessionId) { mutableStateOf(false) }

    // The list is laid out bottom-up, so it opens at the newest message and
    // growing items (streamed output, loading images) keep the bottom in place.
    val listState = rememberLazyListState()
    var panelHeight by remember { mutableIntStateOf(0) }
    // Opened from a notification: the newest message is what the notification
    // announced, so park it on its first line instead of its tail. Holds until
    // the reader scrolls somewhere themselves.
    var readFromStart by rememberSaveable(state.sessionId) { mutableStateOf(focusLatest) }
    LaunchedEffect(listState) {
        listState.interactionSource.interactions.collect {
            if (it is DragInteraction.Start) readFromStart = false
        }
    }
    // Whether changes scroll the newest message into view. Only the reader's
    // own scrolls decide it: where a drag (and its fling) comes to rest.
    var followNewest by remember { mutableStateOf(true) }
    LaunchedEffect(listState) {
        var dragged = false
        launch {
            listState.interactionSource.interactions.collect { if (it is DragInteraction.Start) dragged = true }
        }
        snapshotFlow { listState.isScrollInProgress }.collect { scrolling ->
            if (!scrolling && dragged) {
                dragged = false
                followNewest = listState.firstVisibleItemIndex == 0
            }
        }
    }
    // The session and its messages arrive from the database a few frames after
    // the screen opens, while it is still sliding in. What that first load
    // brings (the messages, the resume bar, the pinned panel) is put in place;
    // only changes after it are animated.
    var settled by remember { mutableStateOf(false) }
    LaunchedEffect(messages.isNotEmpty()) {
        if (messages.isNotEmpty() && !settled) {
            delay(SETTLE_MS)
            settled = true
        }
    }
    val lastId = messages.lastOrNull()?.id
    var prevLastId by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(messages) {
        val prev = prevLastId
        prevLastId = lastId
        if (lastId == null) return@LaunchedEffect
        if (readFromStart) {
            if (lastId != prev) listState.showNewestFromStart(pending.size) { panelHeight }
            return@LaunchedEffect
        }
        if (prev == null) return@LaunchedEffect
        if (followNewest) listState.animateScrollToItem(0)
    }
    // A message just written goes to the bottom, and so does the reader.
    val newestPending = pending.lastOrNull()?.localId
    var prevPending by remember { mutableStateOf(newestPending) }
    LaunchedEffect(newestPending) {
        val prev = prevPending
        prevPending = newestPending
        if (newestPending == null || newestPending == prev) return@LaunchedEffect
        readFromStart = false
        followNewest = true
        listState.animateScrollToItem(0)
    }

    // Tool calls between two reports share one row.
    val rows = remember(messages, state.messageKeys) { transcriptRows(messages, state.messageKeys) }
    // Pending messages sit below the conversation, as the first items of the
    // bottom-up list; row indexes in the list are shifted by them.
    val tail = pending.size
    val stack by remember(messages, rows, tail) {
        derivedStateOf {
            val info = listState.layoutInfo
            val top = info.visibleItemsInfo.maxOfOrNull { it.index }
            if (top == null) {
                FlowStack.EMPTY
            } else {
                val r = rows.lastIndex - (top - tail)
                flowStack(messages, if (r > rows.lastIndex) messages.size else rows.getOrNull(r)?.messageIndex ?: 0)
            }
        }
    }
    val scope = rememberCoroutineScope()
    // While a row opens or closes, the rows above it move with it frame by
    // frame. Animated as placements they would trail behind and overlap it.
    var resizing by remember { mutableStateOf(false) }
    var resizeCount by remember { mutableIntStateOf(0) }
    LaunchedEffect(resizeCount) {
        if (resizeCount == 0) return@LaunchedEffect
        delay(RESIZE_MS + 100L)
        resizing = false
    }
    val onRowResize: () -> Unit = remember {
        {
            resizing = true
            resizeCount++
        }
    }
    // A row can also change in place, as when a question is answered or a
    // message fails to send.
    val rowsResized = pausePlacementOnChange(rows) { a, b -> changedInPlace(a, b) { it.key } }
    val pendingResized = pausePlacementOnChange(pending) { a, b -> changedInPlace(a, b) { it.localId } }
    val pausePlacement = resizing || rowsResized || pendingResized

    Scaffold(
        topBar = {
            TopAppBar(
                expandedHeight = 52.dp,
                navigationIcon = {
                    if (state.showBackButton) {
                        IconButton(onClick = { onAction(SessionDetailAction.Back) }) {
                            Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                        }
                    }
                },
                title = {
                    Column(Modifier.clickable { detailsOpen = !detailsOpen }) {
                        Text(
                            session?.headline() ?: state.sessionId,
                            style = MaterialTheme.typography.titleSmall,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                        session?.let {
                            val st = statusStyle(it.status)
                            val details = listOfNotNull(
                                agentSettingsLabel(it.model, it.effort, it.mode),
                                costLabel(it.costUsd, it.costEstimated),
                            ).joinToString(" · ")
                            val statusColor by animateColorAsState(st.color, label = "status")
                            Row {
                                Text("${st.symbol} ${st.label}", color = statusColor, style = MaterialTheme.typography.labelSmall)
                                if (details.isNotEmpty()) {
                                    Text(
                                        " · $details",
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        style = MaterialTheme.typography.labelSmall,
                                        maxLines = 1,
                                        overflow = TextOverflow.Ellipsis,
                                    )
                                }
                            }
                        }
                    }
                },
                actions = {
                    ContextGauge(session?.contextUsedPercent, onClick = { detailsOpen = !detailsOpen })
                    session?.let { s ->
                        val modeChangeSupported = s.provider == "claude" || s.provider == "codex"
                        Box {
                            IconButton(onClick = { menu = true }) {
                                Icon(Icons.Default.MoreVert, contentDescription = "Session actions")
                            }
                            DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                                DropdownMenuItem(
                                    text = { Text("Terminal") },
                                    leadingIcon = { Icon(Icons.Default.Terminal, contentDescription = null) },
                                    enabled = s.paneId != null,
                                    onClick = {
                                        menu = false
                                        onAction(SessionDetailAction.OpenTerminal)
                                    },
                                )
                                if (modeChangeSupported) {
                                    DropdownMenuItem(
                                        text = { Text("Change mode") },
                                        enabled = s.live && s.paneId != null && !state.actionBusy && !state.modeChanging,
                                        onClick = {
                                            menu = false
                                            onAction(SessionDetailAction.CycleMode)
                                        },
                                    )
                                }
                                SessionActionMenuItems(
                                    s = s.toSessionRef(),
                                    onResume = { onAction(SessionDetailAction.Resume) },
                                    onUnarchive = { onAction(SessionDetailAction.Unarchive) },
                                    onArchive = { onAction(SessionDetailAction.Archive) },
                                    onDismiss = { menu = false },
                                )
                            }
                        }
                    }
                },
            )
        },
        bottomBar = {
            // The composer gives way to the resume bar when the agent stops,
            // and comes back when it runs again: slide the newcomer up.
            AnimatedContent(
                targetState = session != null && session.status == Status.OFFLINE,
                transitionSpec = {
                    if (settled) {
                        (fadeIn(tween(200, delayMillis = 60)) + slideInVertically(tween(260)) { it / 3 })
                            .togetherWith(fadeOut(tween(120)))
                            .using(SizeTransform(clip = false))
                    } else {
                        EnterTransition.None togetherWith ExitTransition.None
                    }
                },
                label = "bottomBar",
            ) { offline ->
                if (offline) {
                    ResumeBar(busy = state.actionBusy, onResume = { onAction(SessionDetailAction.Resume) })
                } else {
                    Composer(
                        enabled = session?.canSend == true && !answering,
                        busy = answering,
                        attachments = state.attachments,
                        attachmentsEnabled = state.attachmentsEnabled,
                        sendOnEnter = state.sendOnEnter,
                        sendWithModifier = state.sendWithModifier,
                        resolveAttachmentPreview = resolveAttachmentPreview,
                        composerModifier = composerModifier,
                        attachmentDropActive = attachmentDropActive,
                        onPasteImage = onPasteImage,
                        onAction = onAction,
                    )
                }
            }
        },
    ) { padding ->
        Column(Modifier.padding(padding).fillMaxSize()) {
            ExpandingContent(value = session?.takeIf { detailsOpen }) {
                Column {
                    (it.cwd?.takeIf { p -> p.isNotBlank() } ?: it.project).takeIf { p -> p.isNotBlank() }?.let { dir ->
                        Text(
                            dir,
                            style = MaterialTheme.typography.labelSmall,
                            fontFamily = FontFamily.Monospace,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1,
                            overflow = TextOverflow.StartEllipsis,
                            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp),
                        )
                    }
                    ContextBar(it.contextUsedTokens, it.contextWindowTokens, it.contextUsedPercent)
                    HorizontalDivider()
                }
            }
            ExpandingContent(value = error) {
                Text(
                    it,
                    color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
                )
            }
            SyncIndicator(
                visible = state.loading && session == null && messages.isEmpty(),
                label = "Loading…",
            )
            Box(Modifier.fillMaxSize()) {
                CompositionLocalProvider(LocalRowResize provides onRowResize) {
                    LazyColumn(
                        state = listState,
                        modifier = Modifier.fillMaxSize(),
                        contentPadding = PaddingValues(vertical = 8.dp),
                        reverseLayout = true,
                    ) {
                        itemsIndexed(pending.asReversed(), key = { _, p -> "pending:" + p.localId }) { r, p ->
                            val i = pending.lastIndex - r
                            PendingMessageItem(
                                modifier = Modifier
                                    .animateContentSize(tween(ITEM_ANIMATION_MS))
                                    .animateItem(placementSpec = if (pausePlacement) null else tween(ITEM_ANIMATION_MS)),
                                message = p,
                                showRole = i == 0 && messages.lastOrNull()?.role != "user",
                                resolveAttachmentPreview = resolveAttachmentPreview,
                                onRetry = { onAction(SessionDetailAction.Retry(p.localId)) },
                                onDiscard = { onAction(SessionDetailAction.Discard(p.localId)) },
                            )
                        }
                        itemsIndexed(rows.asReversed(), key = { _, row -> row.key }) { r, row ->
                            val i = rows.lastIndex - r
                            // Only a sent message is sized here, as it takes the place of
                            // its pending row. Other rows animate their own changes;
                            // sizing them again would chase that and drag it out.
                            val sizeModifier = if (row is MessageRow && row.message.role == "user") {
                                Modifier.animateContentSize(tween(ITEM_ANIMATION_MS))
                            } else {
                                Modifier
                            }
                            val itemModifier = if (settled) {
                                sizeModifier.animateItem(placementSpec = if (pausePlacement) null else tween(ITEM_ANIMATION_MS))
                            } else {
                                sizeModifier
                            }
                            val showRole = i == 0 || rows[i - 1].speaker != row.speaker
                            val providerName = session?.providerName ?: "Agent"
                            val onOpenFile = { path: String, line: Int -> onAction(SessionDetailAction.OpenFile(path, line)) }
                            val onOpenImage = { url: String -> onAction(SessionDetailAction.OpenImage(url)) }
                            val onOpenTerminal = { onAction(SessionDetailAction.OpenTerminal) }
                            val onRespond = { it: InteractionResponseDto -> onAction(SessionDetailAction.Respond(it)) }
                            when (row) {
                                is MessageRow -> MessageItem(
                                    modifier = itemModifier,
                                    message = row.message,
                                    showRole = showRole,
                                    providerName = providerName,
                                    resolveUrl = resolveUrl,
                                    formatFileSize = formatFileSize,
                                    onOpenFile = onOpenFile,
                                    onOpenImage = onOpenImage,
                                    onOpenTerminal = onOpenTerminal,
                                    interactionsEnabled = !answering,
                                    onRespond = onRespond,
                                )
                                is ActivityRow -> ActivityGroupItem(
                                    row = row,
                                    modifier = itemModifier,
                                    showRole = showRole,
                                    providerName = providerName,
                                    resolveUrl = resolveUrl,
                                    formatFileSize = formatFileSize,
                                    onOpenFile = onOpenFile,
                                    onOpenImage = onOpenImage,
                                    onOpenTerminal = onOpenTerminal,
                                    interactionsEnabled = !answering,
                                    onRespond = onRespond,
                                )
                            }
                        }
                    }
                }
                PinnedFlowStack(
                    stack = stack,
                    animate = settled,
                    providerName = session?.providerName ?: "Agent",
                    onHeight = { panelHeight = it },
                    onJump = { id ->
                        val i = rows.indexOfFirst { it is MessageRow && it.message.id == id }
                        if (i >= 0) {
                            readFromStart = false
                            followNewest = i == rows.lastIndex
                            scope.launch { listState.animateScrollToItem(tail + rows.lastIndex - i) }
                        }
                    },
                )
            }
        }
    }
}

private const val SETTLE_MS = 400L

private const val ITEM_ANIMATION_MS = 260

@Composable
private fun BoxScope.PinnedFlowStack(
    stack: FlowStack,
    animate: Boolean,
    providerName: String,
    onHeight: (Int) -> Unit,
    onJump: (String) -> Unit,
) {
    var shownStack by remember { mutableStateOf(stack) }
    if (!stack.isEmpty) shownStack = stack
    AnimatedVisibility(
        visible = !stack.isEmpty,
        enter = if (animate) slideInVertically { -it } + fadeIn() else EnterTransition.None,
        exit = slideOutVertically { -it } + fadeOut(),
        modifier = Modifier.align(Alignment.TopCenter).onSizeChanged { onHeight(it.height) },
    ) {
        FlowStackPanel(stack = shownStack, providerName = providerName, onJump = onJump)
    }
}

fun messageStartOffset(messageHeight: Int, viewportHeight: Int, panelHeight: Int): Int =
    (messageHeight - (viewportHeight - panelHeight)).coerceAtLeast(0)

private suspend fun LazyListState.showNewestFromStart(index: Int, panelHeight: () -> Int) {
    scrollToItem(index)
    var applied = 0
    repeat(3) {
        val info = layoutInfo
        if (info.viewportSize.height <= 0) return
        val height = info.visibleItemsInfo.firstOrNull { it.index == index }?.size ?: return
        val offset = messageStartOffset(height, info.viewportSize.height, panelHeight())
        if (offset == applied) return
        applied = offset
        scrollToItem(index, offset)
        withFrameNanos {}
        withFrameNanos {}
    }
}

@Composable
private fun Composer(
    enabled: Boolean,
    busy: Boolean,
    attachments: List<AttachmentUiState>,
    attachmentsEnabled: Boolean,
    sendOnEnter: Boolean,
    sendWithModifier: Boolean,
    resolveAttachmentPreview: (String) -> Any?,
    composerModifier: Modifier,
    attachmentDropActive: Boolean,
    onPasteImage: (() -> Boolean)?,
    onAction: (SessionDetailAction) -> Unit,
) {
    var text by rememberSaveable { mutableStateOf("") }
    Surface(
        modifier = composerModifier.fillMaxWidth(),
        tonalElevation = 3.dp,
        border = if (attachmentDropActive) BorderStroke(2.dp, MaterialTheme.colorScheme.primary) else null,
    ) {
        Column(Modifier.navigationBarsPadding().imePadding().padding(8.dp)) {
            if (attachmentDropActive) {
                Text(
                    "Drop files to attach",
                    color = MaterialTheme.colorScheme.primary,
                    style = MaterialTheme.typography.labelMedium,
                    modifier = Modifier.fillMaxWidth().padding(bottom = 6.dp),
                )
            }
            AttachmentPreviewRow(
                attachments = attachments,
                resolvePreview = resolveAttachmentPreview,
                onRemove = { onAction(SessionDetailAction.RemoveAttachment(it)) },
            )
            Row(verticalAlignment = Alignment.CenterVertically) {
                AttachButton(
                    enabled = enabled && attachmentsEnabled,
                    onPickImage = { onAction(SessionDetailAction.PickImage) },
                    onPickFile = { onAction(SessionDetailAction.PickFile) },
                )
                OutlinedTextField(
                    value = text,
                    onValueChange = { text = it },
                    enabled = enabled,
                    textStyle = MaterialTheme.typography.bodyMedium,
                    placeholder = {
                        Text(
                            if (enabled || busy) "Message…" else "Session is not running in Herdr",
                            style = MaterialTheme.typography.bodyMedium,
                        )
                    },
                    maxLines = 6,
                    modifier = Modifier.weight(1f).onPreviewKeyEvent { event ->
                        if (event.type != KeyEventType.KeyDown) {
                            false
                        } else if (event.key == Key.V && event.isMetaPressed && !event.isShiftPressed && onPasteImage?.invoke() == true) {
                            true
                        } else if (event.key != Key.Enter) {
                            false
                        } else {
                            val plainEnter = sendOnEnter && !event.isShiftPressed && !event.isMetaPressed && !event.isCtrlPressed
                            val modifiedEnter = sendWithModifier && (event.isMetaPressed || event.isCtrlPressed)
                            if (!plainEnter && !modifiedEnter) {
                                false
                            } else if (enabled && (text.isNotBlank() || attachments.isNotEmpty())) {
                                onAction(SessionDetailAction.Send(text))
                                text = ""
                                true
                            } else {
                                false
                            }
                        }
                    },
                )
                SwapContent(busy) { spinning ->
                    if (spinning) {
                        CircularProgressIndicator(Modifier.padding(12.dp).size(24.dp))
                    } else {
                        IconButton(
                            enabled = enabled && (text.isNotBlank() || attachments.isNotEmpty()),
                            onClick = {
                                onAction(SessionDetailAction.Send(text))
                                text = ""
                            },
                        ) { Icon(Icons.AutoMirrored.Filled.Send, contentDescription = "Send") }
                    }
                }
            }
        }
    }
}

@Composable
private fun ResumeBar(busy: Boolean, onResume: () -> Unit) {
    Surface(tonalElevation = 3.dp) {
        Row(
            Modifier.fillMaxWidth().navigationBarsPadding().padding(horizontal = 16.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                "Not running in Herdr",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.weight(1f),
            )
            Button(onClick = onResume, enabled = !busy) {
                SwapContent(busy) { spinning ->
                    if (spinning) {
                        CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                    } else {
                        Icon(Icons.Default.PlayArrow, contentDescription = null)
                    }
                }
                Text("  Resume")
            }
        }
    }
}
