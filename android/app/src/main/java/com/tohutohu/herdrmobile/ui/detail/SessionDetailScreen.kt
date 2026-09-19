package com.tohutohu.herdrmobile.ui.detail

import android.app.Application
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.EnterTransition
import androidx.compose.animation.ExitTransition
import androidx.compose.animation.SizeTransform
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.DragInteraction
import androidx.compose.foundation.layout.Arrangement
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
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.AttachFile
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Image
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Terminal
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
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
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateListOf
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import coil3.compose.AsyncImage
import com.tohutohu.herdrmobile.container
import com.tohutohu.herdrmobile.data.Attachment
import com.tohutohu.herdrmobile.data.readAttachment
import com.tohutohu.herdrmobile.data.api.Status
import com.tohutohu.herdrmobile.data.db.SessionEntity
import com.tohutohu.herdrmobile.ui.ContextBar
import com.tohutohu.herdrmobile.ui.ContextGauge
import com.tohutohu.herdrmobile.ui.ExpandingContent
import com.tohutohu.herdrmobile.ui.SwapContent
import com.tohutohu.herdrmobile.ui.agentSettingsLabel
import com.tohutohu.herdrmobile.ui.costLabel
import com.tohutohu.herdrmobile.ui.sessions.SessionRef
import com.tohutohu.herdrmobile.ui.sessions.rememberSessionActions
import com.tohutohu.herdrmobile.ui.statusStyle
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SessionDetailScreen(
    sessionId: String,
    focusLatest: Boolean = false,
    onBack: () -> Unit,
    onOpenFile: (String, Int) -> Unit,
    onOpenImage: (String) -> Unit,
    onOpenTerminal: () -> Unit,
) {
    val context = LocalContext.current
    val vm: SessionDetailViewModel = viewModel(key = sessionId) {
        SessionDetailViewModel(context.applicationContext as Application, sessionId)
    }
    val api = context.container.api
    val session by vm.session.collectAsState()
    val actions = rememberSessionActions(onChanged = { vm.refresh() })
    actions.Dialogs()
    var menu by remember { mutableStateOf(false) }
    // The header carries only what changes; the directory and the context bar
    // open under it when the title or the context ring is tapped.
    var detailsOpen by rememberSaveable(sessionId) { mutableStateOf(false) }
    val messages by vm.messages.collectAsState()
    val error by vm.error.collectAsState()
    val pending by vm.pending.collectAsState()
    val answering by vm.answering.collectAsState()
    val lifecycle = LocalLifecycleOwner.current.lifecycle

    LaunchedEffect(lifecycle, sessionId) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) { vm.pollWhileVisible() }
    }

    // The list is laid out bottom-up, so it opens at the newest message and
    // growing items (streamed output, loading images) keep the bottom in place.
    val listState = rememberLazyListState()
    // Messages hidden above the viewport or under the pinned panel feed the panel.
    var panelHeight by remember { mutableIntStateOf(0) }
    // Opened from a notification: the newest message is what the notification
    // announced, so park it on its first line instead of its tail. Holds until
    // the reader scrolls somewhere themselves.
    var readFromStart by rememberSaveable(sessionId) { mutableStateOf(focusLatest) }
    LaunchedEffect(listState) {
        listState.interactionSource.interactions.collect {
            if (it is DragInteraction.Start) readFromStart = false
        }
    }
    // Whether changes scroll the newest message into view. Only the reader's
    // own scrolls decide it: where a drag (and its fling) comes to rest. The
    // list's position right after a change can't tell, because the list stays
    // on the item it showed while messages are added or shrink below it.
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
        // New messages, and also an answered card that disappears or settles
        // without a new message after it.
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

    // Pending messages sit below the conversation, as the first items of the
    // bottom-up list; message indexes in the list are shifted by them.
    val tail = pending.size
    val stack by remember(messages, tail) {
        derivedStateOf {
            val info = listState.layoutInfo
            // Do not use the pinned panel's measured height here. The panel
            // changes height when this stack changes, so using that height to
            // choose the stack creates a feedback loop at the top boundary:
            // reports appear, the panel grows, reports disappear, and so on.
            // The oldest visible message is a stable boundary; the panel can
            // overlay it briefly, just like any other pinned content.
            val top = info.visibleItemsInfo.maxOfOrNull { it.index }
            if (top == null) {
                FlowStack.EMPTY
            } else {
                flowStack(messages, messages.lastIndex - (top - tail))
            }
        }
    }
    val scope = rememberCoroutineScope()

    Scaffold(
        topBar = {
            TopAppBar(
                expandedHeight = 52.dp,
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                title = {
                    Column(Modifier.clickable { detailsOpen = !detailsOpen }) {
                        Text(
                            session?.headline() ?: sessionId,
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
                                        onOpenTerminal()
                                    },
                                )
                                actions.MenuItems(s.ref()) { menu = false }
                            }
                        }
                    }
                },
            )
        },
        bottomBar = {
            val s = session
            // The composer gives way to the resume bar when the agent stops,
            // and comes back when it runs again: slide the newcomer up.
            AnimatedContent(
                targetState = s != null && s.status == Status.OFFLINE,
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
                    ResumeBar(busy = s != null && actions.busy(s.id), onResume = { s?.let { actions.resume(it.ref()) } })
                } else {
                    Composer(
                        enabled = s?.canSend == true && !answering,
                        busy = answering,
                        onSend = vm::send,
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
            Box(Modifier.fillMaxSize()) {
                LoadingHint(visible = messages.isEmpty())
                LazyColumn(
                    state = listState,
                    modifier = Modifier.fillMaxSize(),
                    contentPadding = PaddingValues(vertical = 8.dp),
                    reverseLayout = true,
                ) {
                    itemsIndexed(pending.asReversed(), key = { _, p -> "pending:" + p.localId }) { r, p ->
                        val i = pending.lastIndex - r
                        PendingMessageItem(
                            modifier = Modifier.animateItem(),
                            message = p,
                            showRole = i == 0 && messages.lastOrNull()?.role != "user",
                            onRetry = { vm.retry(p.localId) },
                            onDiscard = { vm.discard(p.localId) },
                        )
                    }
                    itemsIndexed(messages.asReversed(), key = { _, m -> m.id }) { r, m ->
                        val i = messages.lastIndex - r
                        MessageItem(
                            modifier = if (settled) Modifier.animateItem() else Modifier,
                            message = m,
                            showRole = i == 0 || messages[i - 1].role != m.role,
                            providerName = session?.providerName ?: "Agent",
                            resolveUrl = api::absolute,
                            onOpenFile = onOpenFile,
                            onOpenImage = { onOpenImage(api.absolute(it)) },
                            onOpenTerminal = onOpenTerminal,
                            interactionsEnabled = !answering,
                            onRespond = vm::respond,
                        )
                    }
                }
                PinnedFlowStack(
                    stack = stack,
                    animate = settled,
                    providerName = session?.providerName ?: "Agent",
                    onHeight = { panelHeight = it },
                    onJump = { id ->
                        val i = messages.indexOfFirst { it.id == id }
                        if (i >= 0) {
                            readFromStart = false
                            followNewest = i == messages.lastIndex
                            scope.launch { listState.animateScrollToItem(tail + messages.lastIndex - i) }
                        }
                    },
                )
            }
        }
    }
}

/**
 * How long after the first messages arrive the screen counts as loaded: long
 * enough for the screen transition and the scroll to the newest message.
 */
private const val SETTLE_MS = 400L

/**
 * Over the list rather than above it, so it never pushes the messages; and
 * late, so a quick load never shows it at all.
 */
@Composable
private fun LoadingHint(visible: Boolean) {
    AnimatedVisibility(visible = visible, enter = fadeIn(tween(200, delayMillis = 400)), exit = ExitTransition.None) {
        Text("Loading…", modifier = Modifier.padding(16.dp))
    }
}

/** The flow-stack panel, slid in over the top of the list while it has content. */
@Composable
private fun BoxScope.PinnedFlowStack(
    stack: FlowStack,
    animate: Boolean,
    providerName: String,
    onHeight: (Int) -> Unit,
    onJump: (String) -> Unit,
) {
    // Keep the last stack while the panel slides away empty.
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

/**
 * How far the newest message has to be pushed past the bottom edge for its
 * first line to sit right under the pinned panel. Zero once it already fits.
 */
internal fun messageStartOffset(messageHeight: Int, viewportHeight: Int, panelHeight: Int): Int =
    (messageHeight - (viewportHeight - panelHeight)).coerceAtLeast(0)

/**
 * Scrolls the newest message so that reading starts at its first line.
 * The panel only appears once the message above is off screen, so its height
 * is read again after every scroll until the target stops moving.
 */
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
        // One frame for the panel to recompose against the new position,
        // a second one for its measured height to reach panelHeight.
        withFrameNanos {}
        withFrameNanos {}
    }
}

/**
 * Clears as soon as a message is sent: the message itself stays on show above
 * until the conversation has it, so the next one can be written meanwhile.
 * [busy] is an answer to a question or approval on its way.
 */
@Composable
private fun Composer(
    enabled: Boolean,
    busy: Boolean,
    onSend: (String, List<Attachment>) -> Unit,
) {
    var text by rememberSaveable { mutableStateOf("") }
    val attachments = remember { mutableStateListOf<Attachment>() }
    val resolver = LocalContext.current.contentResolver
    val scope = rememberCoroutineScope()
    val add: (List<Uri>) -> Unit = { uris ->
        val fresh = uris.filterNot { uri -> attachments.any { it.uri == uri } }
        if (fresh.isNotEmpty()) {
            scope.launch {
                attachments.addAll(withContext(Dispatchers.IO) { fresh.map { readAttachment(resolver, it) } })
            }
        }
    }
    val imagePicker = rememberLauncherForActivityResult(ActivityResultContracts.PickMultipleVisualMedia(4), add)
    val filePicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments(), add)
    Surface(tonalElevation = 3.dp) {
        Column(Modifier.navigationBarsPadding().imePadding().padding(8.dp)) {
            // A copy, so the row still has its thumbnails while it shrinks away.
            ExpandingContent(value = attachments.toList().takeIf { it.isNotEmpty() }) { shown ->
                LazyRow(horizontalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.padding(bottom = 8.dp)) {
                    items(shown, key = { it.uri.toString() }) { attachment ->
                        Box(Modifier.animateItem()) {
                            if (attachment.isImage) {
                                AsyncImage(model = attachment.uri, contentDescription = null, modifier = Modifier.size(64.dp))
                            } else {
                                FileChip(attachment.name)
                            }
                            IconButton(
                                onClick = { attachments.remove(attachment) },
                                modifier = Modifier.size(24.dp).align(Alignment.TopEnd),
                            ) {
                                Icon(Icons.Default.Close, contentDescription = "Remove")
                            }
                        }
                    }
                }
            }
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box {
                    var menuOpen by remember { mutableStateOf(false) }
                    IconButton(enabled = enabled, onClick = { menuOpen = true }) {
                        Icon(Icons.Default.Add, contentDescription = "Attach")
                    }
                    DropdownMenu(expanded = menuOpen && enabled, onDismissRequest = { menuOpen = false }) {
                        DropdownMenuItem(
                            text = { Text("Image") },
                            leadingIcon = { Icon(Icons.Default.Image, contentDescription = null) },
                            onClick = {
                                menuOpen = false
                                imagePicker.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly))
                            },
                        )
                        DropdownMenuItem(
                            text = { Text("File") },
                            leadingIcon = { Icon(Icons.Default.AttachFile, contentDescription = null) },
                            onClick = {
                                menuOpen = false
                                filePicker.launch(arrayOf("*/*"))
                            },
                        )
                    }
                }
                // Same size as the message text, not the larger text-field default.
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
                    modifier = Modifier.weight(1f),
                )
                SwapContent(busy) { spinning ->
                    if (spinning) {
                        CircularProgressIndicator(Modifier.padding(12.dp).size(24.dp))
                    } else {
                        IconButton(
                            enabled = enabled && (text.isNotBlank() || attachments.isNotEmpty()),
                            onClick = {
                                onSend(text, attachments.toList())
                                text = ""
                                attachments.clear()
                            },
                        ) { Icon(Icons.AutoMirrored.Filled.Send, contentDescription = "Send") }
                    }
                }
            }
        }
    }
}

/** Stands in for the thumbnail of an attachment that has nothing to show. */
@Composable
private fun FileChip(name: String) {
    Surface(
        color = MaterialTheme.colorScheme.surfaceVariant,
        shape = MaterialTheme.shapes.small,
        modifier = Modifier.height(64.dp).widthIn(max = 160.dp),
    ) {
        Row(
            Modifier.padding(horizontal = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            Icon(Icons.Default.AttachFile, contentDescription = null, modifier = Modifier.size(16.dp))
            Text(
                name,
                style = MaterialTheme.typography.labelSmall,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

private fun SessionEntity.ref() = SessionRef(id, live = status != Status.OFFLINE, archived = archived)

/**
 * The header's first line: the session's own name, which says more than the
 * agent and the folder do. Both of those are still reachable - the model name
 * sits right below it and the directory is one tap away - so they only stand
 * in when the agent has not named the session yet.
 */
private fun SessionEntity.headline(): String =
    title?.takeIf { it.isNotBlank() } ?: project.takeIf { it.isNotBlank() } ?: cwd?.takeIf { it.isNotBlank() } ?: id

/** Shown instead of the composer while the session is not running. */
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
