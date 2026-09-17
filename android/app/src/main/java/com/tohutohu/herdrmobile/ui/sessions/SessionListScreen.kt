package com.tohutohu.herdrmobile.ui.sessions

import android.text.format.DateUtils
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
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
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
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
import com.tohutohu.herdrmobile.ui.statusStyle
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

private const val LIST_POLL_MS = 5_000L

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SessionListScreen(onOpen: (String) -> Unit, onSettings: () -> Unit, onNew: () -> Unit, onArchived: () -> Unit) {
    val repo = LocalContext.current.container.repository
    val sessionsFlow = remember(repo) { repo.observeSessions() }
    val sessions by sessionsFlow.collectAsState(initial = emptyList())
    var error by remember { mutableStateOf<String?>(null) }
    var refreshing by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val lifecycle = LocalLifecycleOwner.current.lifecycle

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
    BackHandler(selection.active) { selection.clear() }

    // Keyed items keep the scroll anchor, which would hide sessions that
    // appear above the first row; stay at the top when the user is there.
    val listState = rememberLazyListState()
    val firstId = sessions.firstOrNull()?.id
    LaunchedEffect(firstId) {
        if (!selection.active && listState.firstVisibleItemIndex <= 1) listState.scrollToItem(0)
    }
    val actions = rememberSessionActions(onChanged = { refresh() })
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
            if (selection.active) {
                SelectionTopBar(selection, refs, actions)
            } else {
                TopAppBar(
                    title = { Text("Sessions") },
                    actions = {
                        IconButton(onClick = onArchived) { Icon(Icons.Default.Inventory2, contentDescription = "Archived sessions") }
                        IconButton(onClick = onSettings) { Icon(Icons.Default.Settings, contentDescription = "Settings") }
                    },
                )
            }
        },
        floatingActionButton = {
            if (!selection.active) {
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
            LazyColumn(Modifier.fillMaxSize(), state = listState) {
                error?.let {
                    item {
                        Text(
                            "Gateway unreachable: $it",
                            color = MaterialTheme.colorScheme.error,
                            style = MaterialTheme.typography.bodySmall,
                            modifier = Modifier.padding(16.dp),
                        )
                    }
                }
                if (sessions.isEmpty() && error == null) {
                    item {
                        Text(
                            "No sessions. Start Claude Code or Codex inside Herdr on your Mac.",
                            modifier = Modifier.padding(16.dp),
                        )
                    }
                }
                items(sessions, key = { it.id }) { s ->
                    SessionRow(
                        s,
                        busy = actions.busy(s.id),
                        selected = selection.contains(s.id),
                        selecting = selection.active,
                        onClick = { if (selection.active) selection.toggle(s.id) else onOpen(s.id) },
                        onLongClick = { selection.toggle(s.id) },
                    )
                    HorizontalDivider()
                }
            }
        }
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
    Box {
        Column(
            Modifier
                .fillMaxWidth()
                .background(if (selected) MaterialTheme.colorScheme.secondaryContainer else Color.Transparent)
                .combinedClickable(onClick = onClick, onLongClick = onLongClick, onLongClickLabel = "Select session")
                .padding(horizontal = 16.dp, vertical = 12.dp),
            verticalArrangement = Arrangement.spacedBy(2.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (selecting) {
                    Icon(
                        if (selected) Icons.Default.CheckCircle else Icons.Default.RadioButtonUnchecked,
                        contentDescription = null,
                        tint = if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.outline,
                        modifier = Modifier.padding(end = 6.dp).size(16.dp),
                    )
                }
                Text(s.providerName, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary)
                agentSettingsLabel(s.model, s.effort, s.mode)?.let {
                    Text(
                        " · $it",
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                    )
                }
                Text(
                    "  " + DateUtils.getRelativeTimeSpanString(s.updatedAt),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.weight(1f),
                )
                Text("${style.symbol} ${style.label}", color = style.color, style = MaterialTheme.typography.labelMedium)
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
