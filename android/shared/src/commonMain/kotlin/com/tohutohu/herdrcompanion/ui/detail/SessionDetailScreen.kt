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
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.DragInteraction
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.ColumnScope
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
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Stop
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isMetaPressed
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.tohutohu.herdrcompanion.data.SendState
import com.tohutohu.herdrcompanion.data.api.InteractionResponseDto
import com.tohutohu.herdrcompanion.data.api.Status
import com.tohutohu.herdrcompanion.model.headline
import com.tohutohu.herdrcompanion.ui.BACKGROUND_SYMBOL
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
    /** When a turn ended, as a clock time (with the date unless it is today). */
    formatTime: (Long) -> String,
    resolveAttachmentPreview: (String) -> Any? = { null },
    onPasteImage: (() -> Boolean)? = null,
    /** Applied to the top bar, e.g. to make it a drag handle on the desktop. */
    headerModifier: Modifier = Modifier,
    /**
     * Platform items at the end of the session menu; [dismiss] closes it.
     * With them the menu is shown even before the session has loaded.
     */
    extraMenuItems: (@Composable ColumnScope.(dismiss: () -> Unit) -> Unit)? = null,
    /** Unsent composer text per session, kept while the session is closed. */
    drafts: ComposerDrafts = remember { ComposerDrafts() },
    /** Questions asked on the side (btw); null leaves the feature out. */
    sideQuestions: SideQuestions? = null,
    /**
     * Docks the side conversation under the conversation, in place of the
     * composer (a desktop pane), instead of a modal sheet over the screen.
     */
    sideQuestionsInPane: Boolean = false,
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
    // The model catalog is read only once the pickers are about to show.
    val settingsShown = detailsOpen && session != null && settingsChangeable(session)
    LaunchedEffect(settingsShown, session?.provider) {
        val settings = state.settings
        if (settingsShown && settings.catalog == null && !settings.loading && settings.loadError == null) {
            onAction(SessionDetailAction.LoadSettings)
        }
    }

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
    var followNewest by rememberSaveable(state.sessionId) { mutableStateOf(true) }
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

    // Each turn folds its work into one row, leaving its newest report out.
    val liveTurn = when (session?.status) {
        Status.RUNNING, Status.WAITING_INPUT, Status.WAITING_APPROVAL -> LastTurn.WORKING
        Status.FAILED -> LastTurn.FAILED
        else -> LastTurn.DONE
    }
    // While a message is on its way, the agent can start on it before the
    // conversation shows it. That must not reopen the turn before it, so the
    // newest turn keeps the state it had when the message was sent.
    val sending = pending.any { it.state != SendState.FAILED }
    val heldTurn = remember(sending) { liveTurn }
    val lastTurn = if (sending) heldTurn else liveTurn
    val rows = remember(messages, state.messageKeys, lastTurn) { transcriptRows(messages, state.messageKeys, lastTurn) }
    // Messages on their way sit below the conversation, as the first items
    // of the bottom-up list.
    val entries = remember(pending, rows) {
        pending.asReversed().map { ListEntry.Sent(it) } + rows.asReversed().map { ListEntry.Shown(it) }
    }
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

    // btw: Claude answers questions on the side of a session it can be sent to.
    val sideAvailable = sideQuestions != null && session != null && session.provider == "claude" && session.canSend
    val conversations = sideQuestions?.conversations?.collectAsState()
    val conversation = conversations?.value?.get(state.sessionId) ?: SideConversation()
    val sideOpen = sideAvailable && conversation.open
    var screenHeight by remember { mutableStateOf(0.dp) }
    val density = LocalDensity.current
    if (sideOpen && !sideQuestionsInPane && sideQuestions != null) {
        SideQuestionSheet(state.sessionId, conversation, sideQuestions)
    }

    Scaffold(
        modifier = Modifier.onSizeChanged { screenHeight = with(density) { it.height.toDp() } },
        topBar = {
            TopAppBar(
                modifier = headerModifier,
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
                                if (it.background.isNotEmpty()) {
                                    Text(
                                        " · $BACKGROUND_SYMBOL ${it.background.size} bg",
                                        color = MaterialTheme.colorScheme.tertiary,
                                        style = MaterialTheme.typography.labelSmall,
                                        maxLines = 1,
                                        softWrap = false,
                                    )
                                }
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
                    if (session != null || extraMenuItems != null) {
                        Box {
                            IconButton(onClick = { menu = true }) {
                                Icon(Icons.Default.MoreVert, contentDescription = "Session actions")
                            }
                            DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                                session?.let { s ->
                                    if (s.live && s.status in WORKING_STATUSES) {
                                        DropdownMenuItem(
                                            text = { Text("Interrupt") },
                                            leadingIcon = { Icon(Icons.Default.Stop, contentDescription = null) },
                                            enabled = !state.actionBusy,
                                            onClick = {
                                                menu = false
                                                onAction(SessionDetailAction.Interrupt)
                                            },
                                        )
                                    }
                                    DropdownMenuItem(
                                        text = { Text("Reload") },
                                        leadingIcon = { Icon(Icons.Default.Refresh, contentDescription = null) },
                                        enabled = !state.reloading,
                                        onClick = {
                                            menu = false
                                            onAction(SessionDetailAction.Reload)
                                        },
                                    )
                                    SessionActionMenuItems(
                                        s = s.toSessionRef(),
                                        onResume = { onAction(SessionDetailAction.Resume) },
                                        onUnarchive = { onAction(SessionDetailAction.Unarchive) },
                                        onArchive = { onAction(SessionDetailAction.Archive) },
                                        onDismiss = { menu = false },
                                    )
                                }
                                extraMenuItems?.let { items ->
                                    if (session != null) HorizontalDivider()
                                    items { menu = false }
                                }
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
                targetState = when {
                    session != null && session.status == Status.OFFLINE -> BottomBar.RESUME
                    sideOpen && sideQuestionsInPane -> BottomBar.SIDE_QUESTIONS
                    else -> BottomBar.COMPOSER
                },
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
            ) { bar ->
                if (bar == BottomBar.RESUME) {
                    ResumeBar(busy = state.actionBusy, onResume = { onAction(SessionDetailAction.Resume) })
                } else if (bar == BottomBar.SIDE_QUESTIONS && sideQuestions != null) {
                    SideQuestionPane(state.sessionId, conversation, sideQuestions, paneHeight = screenHeight)
                } else {
                    Composer(
                        sessionId = state.sessionId,
                        drafts = drafts,
                        enabled = session?.canSend == true && !answering,
                        busy = answering,
                        attachments = state.attachments,
                        attachmentsEnabled = state.attachmentsEnabled,
                        sendOnEnter = state.sendOnEnter,
                        sendWithModifier = state.sendWithModifier,
                        resolveAttachmentPreview = resolveAttachmentPreview,
                        onPasteImage = onPasteImage,
                        onAskOnTheSide = if (sideAvailable && sideQuestions != null) {
                            { sideQuestions.setOpen(state.sessionId, true) }
                        } else {
                            null
                        },
                        // Only a side conversation with something in it gets a way back beside send.
                        sideQuestion = if (sideAvailable && sideQuestions != null && conversation.lanes.isNotEmpty()) {
                            { SideQuestionButton(conversation, onClick = { sideQuestions.toggle(state.sessionId) }) }
                        } else {
                            null
                        },
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
                    if (settingsChangeable(it)) SessionSettingsPanel(it, state.settings, onAction)
                    HorizontalDivider()
                }
            }
            ExpandingContent(value = session?.error?.takeIf { it.isNotBlank() }) {
                Text(
                    it,
                    color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
                )
            }
            ExpandingContent(value = error) {
                Text(
                    it,
                    color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
                )
            }
            ExpandingContent(value = state.reloadNotice) {
                ReloadNoticeRow(it, onDismiss = { onAction(SessionDetailAction.DismissReloadNotice) })
            }
            SyncIndicator(
                visible = state.reloading || (state.loading && session == null && messages.isEmpty()),
                label = if (state.reloading) "Reloading…" else "Loading…",
            )
            val pull = rememberBottomPullState { onAction(SessionDetailAction.PullRefresh) }
            Box(
                Modifier
                    .weight(1f)
                    .fillMaxWidth()
                    .clipToBounds()
                    .then(if (state.pullToRefresh) Modifier.nestedScroll(pull.connection) else Modifier),
            ) {
                CompositionLocalProvider(
                    LocalRowResize provides onRowResize,
                    LocalAnsweredInteractions provides state.answeredInteractions,
                ) {
                    LazyColumn(
                        state = listState,
                        modifier = Modifier.fillMaxSize(),
                        contentPadding = PaddingValues(vertical = 8.dp),
                        reverseLayout = true,
                    ) {
                        items(entries, key = { it.key }) { entry ->
                            val sent = (entry as? ListEntry.Sent)?.message
                            val row = (entry as? ListEntry.Shown)?.row
                            val userMessage = (row as? MessageRow)?.message?.takeIf { it.role == "user" }
                            val placement = if (pausePlacement) null else tween<IntOffset>(ITEM_ANIMATION_MS)
                            val onOpenFile = { path: String, line: Int -> onAction(SessionDetailAction.OpenFile(path, line)) }
                            val onOpenImage = { url: String -> onAction(SessionDetailAction.OpenImage(url)) }
                            val onOpenSession = { id: String -> onAction(SessionDetailAction.OpenSession(id)) }
                            val onRespond = { it: InteractionResponseDto -> onAction(SessionDetailAction.Respond(it)) }
                            if (sent != null || userMessage != null) {
                                // One call for a message on its way and the message it
                                // becomes: with the key it keeps, the bubble changes in
                                // place, and only it is sized here as it settles.
                                UserBubble(
                                    pending = sent != null,
                                    delivery = sent?.delivery ?: Delivery.Queued.takeIf { userMessage?.queued == true },
                                    modifier = Modifier
                                        .animateContentSize(tween(ITEM_ANIMATION_MS))
                                        .then(if (settled || sent != null) Modifier.animateItem(placementSpec = placement) else Modifier),
                                    onRetry = { sent?.let { onAction(SessionDetailAction.Retry(it.localId)) } },
                                    onDiscard = { sent?.let { onAction(SessionDetailAction.Discard(it.localId)) } },
                                ) {
                                    if (sent != null) {
                                        PendingMessageBody(sent, resolveAttachmentPreview)
                                    } else if (userMessage != null) {
                                        MessageBlocks(
                                            userMessage, resolveUrl, formatFileSize, onOpenFile, onOpenImage,
                                            interactionsEnabled = !answering, onRespond = onRespond,
                                            onOpenSession = onOpenSession,
                                        )
                                    }
                                }
                            } else if (row != null) {
                                // Other rows animate their own changes; sizing them
                                // again would chase that and drag it out.
                                TranscriptRowItem(
                                    row = row,
                                    modifier = if (settled) Modifier.animateItem(placementSpec = placement) else Modifier,
                                    formatTime = formatTime,
                                    resolveUrl = resolveUrl,
                                    formatFileSize = formatFileSize,
                                    onOpenFile = onOpenFile,
                                    onOpenImage = onOpenImage,
                                    interactionsEnabled = !answering,
                                    onRespond = onRespond,
                                    onOpenSession = onOpenSession,
                                )
                            }
                        }
                    }
                }
                BottomPullIndicator(pull, refreshing = state.pullRefreshing)
                PinnedFlowStack(
                    stack = stack,
                    animate = settled,
                    providerName = session?.providerName ?: "Agent",
                    onHeight = { panelHeight = it },
                    onJump = { id ->
                        // A report folded into a turn's work is jumped to by its row.
                        val i = rows.indexOfFirst { it.shows(id) }
                        if (i >= 0) {
                            readFromStart = false
                            followNewest = i == rows.lastIndex
                            scope.launch { listState.animateScrollToItem(tail + rows.lastIndex - i) }
                        }
                    },
                )
            }
            ExpandingContent(value = session?.background?.takeIf { it.isNotEmpty() }) {
                BackgroundTasksBar(state.sessionId, it)
            }
        }
    }
}

private const val SETTLE_MS = 400L

private enum class BottomBar { COMPOSER, RESUME, SIDE_QUESTIONS }

/** The agent is in a turn, which a direct session can interrupt. */
private val WORKING_STATUSES = setOf(Status.RUNNING, Status.WAITING_INPUT, Status.WAITING_APPROVAL)

/** An item of the message list. A sent message's key passes to the message it becomes. */
private sealed interface ListEntry {
    val key: String

    data class Sent(val message: PendingMessageUiState) : ListEntry {
        override val key get() = "pending:" + message.localId
    }

    data class Shown(val row: TranscriptRow) : ListEntry {
        override val key get() = row.key
    }
}

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
    sessionId: String,
    drafts: ComposerDrafts,
    enabled: Boolean,
    busy: Boolean,
    attachments: List<AttachmentUiState>,
    attachmentsEnabled: Boolean,
    sendOnEnter: Boolean,
    sendWithModifier: Boolean,
    resolveAttachmentPreview: (String) -> Any?,
    onPasteImage: (() -> Boolean)?,
    /** Opens the side conversation (btw) from the attach menu. */
    onAskOnTheSide: (() -> Unit)?,
    /** The way back to a side conversation, beside send. */
    sideQuestion: (@Composable () -> Unit)?,
    onAction: (SessionDetailAction) -> Unit,
) {
    // Read back from the drafts, so it survives the session being left, going
    // offline (the resume bar replaces the composer) or the app restarting.
    var text by remember(sessionId, drafts) { mutableStateOf(drafts[sessionId]) }
    fun edit(value: String) {
        text = value
        drafts[sessionId] = value
    }
    // Text can arrive from elsewhere too (comments on an HTML page).
    LaunchedEffect(sessionId, drafts) {
        drafts.observe(sessionId).collect { if (it != text) text = it }
    }
    // No bar of its own: the field floats on the conversation's background as
    // one filled pill, with attach and send inside it.
    Surface(
        modifier = Modifier.fillMaxWidth(),
        color = MaterialTheme.colorScheme.background,
    ) {
        Column(Modifier.navigationBarsPadding().imePadding().padding(start = 12.dp, end = 12.dp, top = 4.dp, bottom = 10.dp)) {
            AttachmentPreviewRow(
                attachments = attachments,
                resolvePreview = resolveAttachmentPreview,
                onRemove = { onAction(SessionDetailAction.RemoveAttachment(it)) },
            )
            Row(
                Modifier
                    .fillMaxWidth()
                    .clip(ComposerShape)
                    .background(MaterialTheme.colorScheme.surfaceContainerHigh)
                    .padding(horizontal = 4.dp),
                // Grown to several lines, the buttons stay at the bottom by the thumb.
                verticalAlignment = Alignment.Bottom,
            ) {
                Box(Modifier.padding(bottom = 4.dp)) {
                    AttachButton(
                        enabled = enabled && attachmentsEnabled,
                        onPickImage = { onAction(SessionDetailAction.PickImage) },
                        onPickFile = { onAction(SessionDetailAction.PickFile) },
                        onAskOnTheSide = onAskOnTheSide,
                    )
                }
                val clear = Color.Transparent
                TextField(
                    value = text,
                    onValueChange = { edit(it) },
                    enabled = enabled,
                    textStyle = MaterialTheme.typography.bodyMedium.copy(letterSpacing = 0.sp),
                    placeholder = {
                        Text(
                            if (enabled || busy) "Message…" else "Session is not running",
                            style = MaterialTheme.typography.bodyMedium.copy(letterSpacing = 0.sp),
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    },
                    colors = TextFieldDefaults.colors(
                        focusedContainerColor = clear,
                        unfocusedContainerColor = clear,
                        disabledContainerColor = clear,
                        focusedIndicatorColor = clear,
                        unfocusedIndicatorColor = clear,
                        disabledIndicatorColor = clear,
                    ),
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
                                edit("")
                                true
                            } else {
                                false
                            }
                        }
                    },
                )
                sideQuestion?.let { Box(Modifier.padding(bottom = 4.dp)) { it() } }
                SwapContent(busy, modifier = Modifier.padding(bottom = 4.dp).size(48.dp), iconOnly = true) { spinning ->
                    if (spinning) {
                        CircularProgressIndicator(Modifier.padding(12.dp).size(24.dp))
                    } else {
                        IconButton(
                            enabled = enabled && (text.isNotBlank() || attachments.isNotEmpty()),
                            onClick = {
                                onAction(SessionDetailAction.Send(text))
                                edit("")
                            },
                        ) { Icon(Icons.AutoMirrored.Filled.Send, contentDescription = "Send") }
                    }
                }
            }
        }
    }
}

private val ComposerShape = RoundedCornerShape(28.dp)

@Composable
private fun ResumeBar(busy: Boolean, onResume: () -> Unit) {
    Surface(color = MaterialTheme.colorScheme.background) {
        Row(
            Modifier.fillMaxWidth().navigationBarsPadding().padding(horizontal = 16.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                "Not running",
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

/**
 * The outcome of a manual reload. A missed question stays until dismissed:
 * it is the case the reload exists to catch.
 */
@Composable
private fun ReloadNoticeRow(notice: ReloadNotice, onDismiss: () -> Unit) {
    val color = if (notice.missed) MaterialTheme.colorScheme.tertiary else MaterialTheme.colorScheme.onSurfaceVariant
    Column(Modifier.fillMaxWidth().padding(start = 16.dp, end = 8.dp, top = 4.dp)) {
        Text(notice.text, color = color, style = MaterialTheme.typography.bodySmall)
        if (notice.missed) {
            TextButton(onClick = onDismiss, modifier = Modifier.align(Alignment.End)) { Text("Dismiss") }
        }
    }
}
