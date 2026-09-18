package com.tohutohu.herdrmobile.ui.sessions

import android.text.format.DateUtils
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.tween
import androidx.compose.animation.expandHorizontally
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.scaleIn
import androidx.compose.animation.scaleOut
import androidx.compose.animation.shrinkHorizontally
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyItemScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Inventory2
import androidx.compose.material.icons.filled.RadioButtonUnchecked
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import com.tohutohu.herdrmobile.container
import com.tohutohu.herdrmobile.data.api.Status
import com.tohutohu.herdrmobile.data.db.SessionEntity
import com.tohutohu.herdrmobile.ui.agentSettingsLabel
import com.tohutohu.herdrmobile.ui.contextLabel
import com.tohutohu.herdrmobile.ui.statusStyle
import com.tohutohu.herdrmobile.ui.usage.UsageCard
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.launch
import androidx.navigationevent.NavigationEventInfo
import androidx.navigationevent.compose.NavigationBackHandler
import androidx.navigationevent.compose.rememberNavigationEventState

private const val LIST_POLL_MS = 5_000L

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SessionListScreen(onOpen: (String) -> Unit, onSettings: () -> Unit, onNew: () -> Unit, onArchived: () -> Unit) {
    val container = LocalContext.current.container
    val repo = container.repository
    // null until the cache has answered; then the rows render without a flash.
    val loaded by container.sessions.collectAsState()
    val sessions = loaded.orEmpty()
    var error by remember { mutableStateOf<String?>(null) }
    var refreshing by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val snackbar = remember { SnackbarHostState() }

    suspend fun refresh() {
        error = try {
            repo.refreshSessions()
            null
        } catch (e: Exception) {
            e.message ?: e.toString()
        }
    }
    val selection = rememberSessionSelection()
    val refs = remember(sessions) { sessions.map { it.ref() } }
    LaunchedEffect(refs) { selection.keepOnly(refs.map { it.id }) }
    NavigationBackHandler(
        state = rememberNavigationEventState(currentInfo = NavigationEventInfo.None),
        isBackEnabled = selection.active,
        onBackCompleted = { selection.clear() },
    )

    // Keyed items keep the scroll anchor, which would hide sessions that
    // appear above the first row; stay at the top while the user is there.
    // "There" is where their last scroll left the list, not where the anchor
    // put it, so it is sampled when a scroll settles.
    val listState = rememberLazyListState()
    var pinnedToTop by remember { mutableStateOf(true) }
    LaunchedEffect(listState) {
        snapshotFlow { listState.isScrollInProgress }.filter { !it }.collect {
            pinnedToTop = listState.firstVisibleItemIndex == 0 && listState.firstVisibleItemScrollOffset == 0
        }
    }
    val firstId = sessions.firstOrNull()?.id
    LaunchedEffect(firstId) {
        if (pinnedToTop && !selection.active) listState.scrollToItem(0)
    }
    val actions = rememberSessionActions(snackbar = snackbar, onChanged = { refresh() })
    actions.Dialogs()

    // Poll only while visible; always refresh when returning to foreground.
    LaunchedEffect(lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                refresh()
                delay(LIST_POLL_MS)
            }
        }
    }

    Scaffold(
        topBar = {
            SwitchingTopBar(selecting = selection.active, selectionBar = { SelectionTopBar(selection, refs, actions) }) {
                TopAppBar(
                    title = { Text("Sessions") },
                    actions = {
                        IconButton(onClick = onArchived) { Icon(Icons.Default.Inventory2, contentDescription = "Archived sessions") }
                        IconButton(onClick = onSettings) { Icon(Icons.Default.Settings, contentDescription = "Settings") }
                    },
                )
            }
        },
        snackbarHost = { SnackbarHost(snackbar) },
        floatingActionButton = {
            AnimatedVisibility(
                visible = !selection.active,
                enter = scaleIn() + fadeIn(),
                exit = scaleOut() + fadeOut(),
            ) {
                ExtendedFloatingActionButton(
                    onClick = onNew,
                    icon = { Icon(Icons.Default.Add, contentDescription = null) },
                    text = { Text("New session") },
                )
            }
        },
    ) { padding ->
        PullToRefreshBox(
            isRefreshing = refreshing,
            onRefresh = {
                scope.launch {
                    refreshing = true
                    refresh()
                    refreshing = false
                }
            },
            modifier = Modifier.padding(padding).fillMaxSize(),
        ) {
            Column(Modifier.fillMaxSize()) {
                UsageCard()
                LazyColumn(Modifier.fillMaxSize(), state = listState) {
                    error?.let {
                        item(key = "error") {
                            Text(
                                "Gateway unreachable: $it",
                                color = MaterialTheme.colorScheme.error,
                                style = MaterialTheme.typography.bodySmall,
                                modifier = Modifier.animateItem().padding(16.dp),
                            )
                        }
                    }
                    if (loaded != null && sessions.isEmpty() && error == null) {
                        item(key = "empty") {
                            Text(
                                "No sessions. Start Claude Code or Codex inside Herdr on your Mac.",
                                modifier = Modifier.animateItem().padding(16.dp),
                            )
                        }
                    }
                    items(sessions, key = { it.id }) { s ->
                        SessionItem(s, selection, actions, onOpen)
                    }
                }
            }
        }
    }
}

/**
 * A list entry: the row, swipeable into [swipeActionFor] unless a selection
 * is active, and its divider. Animates its place in the list.
 */
@Composable
internal fun LazyItemScope.SessionItem(
    s: SessionEntity,
    selection: SessionSelection,
    actions: SessionActions,
    onOpen: (String) -> Unit,
) {
    val ref = s.ref()
    Column(Modifier.animateItem()) {
        SwipeableSessionRow(
            action = swipeActionFor(ref),
            engaged = actions.engaged(s.id),
            isEngaged = { actions.engaged(s.id) },
            busy = actions.busy(s.id),
            enabled = !selection.active,
            onSwipe = { if (ref.archived) actions.unarchive(ref) else actions.archive(ref) },
        ) {
            SessionRow(
                s,
                busy = actions.busy(s.id),
                selected = selection.contains(s.id),
                selecting = selection.active,
                onClick = { if (selection.active) selection.toggle(s.id) else onOpen(s.id) },
                onLongClick = { selection.toggle(s.id) },
            )
        }
        HorizontalDivider()
    }
}

/** Fades between the screen's own app bar and the selection bar. */
@Composable
internal fun SwitchingTopBar(selecting: Boolean, selectionBar: @Composable () -> Unit, bar: @Composable () -> Unit) {
    AnimatedContent(
        targetState = selecting,
        transitionSpec = { fadeIn(tween(200)) togetherWith fadeOut(tween(120)) },
        label = "topBar",
    ) { active ->
        if (active) selectionBar() else bar()
    }
}

internal fun SessionEntity.ref() = SessionRef(id, live = status != Status.OFFLINE, archived = archived)

/** A session; long press starts a selection for the batch actions. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
internal fun SessionRow(
    s: SessionEntity,
    busy: Boolean,
    selected: Boolean,
    selecting: Boolean,
    onClick: () -> Unit,
    onLongClick: () -> Unit,
) {
    val style = statusStyle(s.status)
    // Opaque, so the swipe background stays hidden until the row moves.
    val background by animateColorAsState(
        if (selected) MaterialTheme.colorScheme.secondaryContainer else MaterialTheme.colorScheme.surface,
        label = "rowBackground",
    )
    Box {
        Column(
            Modifier
                .fillMaxWidth()
                .background(background)
                .combinedClickable(onClick = onClick, onLongClick = onLongClick, onLongClickLabel = "Select session")
                .padding(horizontal = 16.dp, vertical = 12.dp),
            verticalArrangement = Arrangement.spacedBy(2.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                // Everything but the status shares what the status leaves; the
                // settings label gives way first so the time stays on one line.
                Row(Modifier.weight(1f), verticalAlignment = Alignment.CenterVertically) {
                    AnimatedVisibility(
                        visible = selecting,
                        enter = expandHorizontally() + fadeIn(),
                        exit = shrinkHorizontally() + fadeOut(),
                    ) {
                        Icon(
                            if (selected) Icons.Default.CheckCircle else Icons.Default.RadioButtonUnchecked,
                            contentDescription = null,
                            tint = if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.outline,
                            modifier = Modifier.padding(end = 6.dp).size(16.dp),
                        )
                    }
                    Text(s.providerName, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary, maxLines = 1)
                    listOfNotNull(agentSettingsLabel(s.model, s.effort, s.mode), contextLabel(s.contextUsedPercent))
                        .joinToString(" · ")
                        .takeIf { it.isNotEmpty() }
                        ?.let {
                            Text(
                                " · $it",
                                style = MaterialTheme.typography.labelMedium,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                                modifier = Modifier.weight(1f, fill = false),
                            )
                        }
                    Text(
                        "  " + DateUtils.getRelativeTimeSpanString(s.updatedAt),
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        softWrap = false,
                    )
                }
                Text(
                    "${style.symbol} ${style.label}",
                    color = style.color,
                    style = MaterialTheme.typography.labelMedium,
                    maxLines = 1,
                    modifier = Modifier.padding(start = 8.dp),
                )
            }
            Text(s.project.ifBlank { s.cwd ?: s.id }, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            s.title?.takeIf { it.isNotBlank() }?.let {
                Text(it, style = MaterialTheme.typography.bodyMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            s.lastMessage?.takeIf { it.isNotBlank() }?.let {
                Text(
                    it,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        if (busy) LinearProgressIndicator(Modifier.fillMaxWidth().align(Alignment.BottomCenter))
    }
}
