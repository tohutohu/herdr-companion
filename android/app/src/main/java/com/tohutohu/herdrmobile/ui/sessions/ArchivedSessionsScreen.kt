package com.tohutohu.herdrmobile.ui.sessions

import android.text.format.DateUtils
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrmobile.container
import com.tohutohu.herdrmobile.data.api.SessionDto
import com.tohutohu.herdrmobile.data.toEntity
import com.tohutohu.herdrmobile.ui.toUiModel
import kotlinx.coroutines.launch
import androidx.navigationevent.NavigationEventInfo
import androidx.navigationevent.compose.NavigationBackHandler
import androidx.navigationevent.compose.rememberNavigationEventState
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.PaddingValues
import com.tohutohu.herdrmobile.ui.exceptBottom

/**
 * Archived sessions, read directly from the gateway (not cached). Swipe a
 * session sideways to unarchive it, or long press to select several and
 * unarchive or resume from the selection bar.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ArchivedSessionsScreen(onBack: () -> Unit, onOpen: (String) -> Unit) {
    val repo = LocalContext.current.container.repository
    val scope = rememberCoroutineScope()
    var sessions by remember { mutableStateOf<List<SessionDto>?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var refreshing by remember { mutableStateOf(false) }
    val snackbar = remember { SnackbarHostState() }

    suspend fun load() {
        try {
            sessions = repo.archivedSessions()
            error = null
        } catch (e: Exception) {
            error = e.message ?: e.toString()
        }
    }

    val actions = rememberSessionActions(snackbar = snackbar, onChanged = { load() })
    actions.Dialogs()
    LaunchedEffect(Unit) { load() }

    val rows = remember(sessions) {
        val now = System.currentTimeMillis()
        sessions.orEmpty().map {
            val entity = it.toEntity(now, listed = false)
            SessionListItemUiState(entity.toUiModel(), DateUtils.getRelativeTimeSpanString(entity.updatedAt).toString())
        }
    }
    val selection = rememberSessionSelection()
    val refs = remember(rows) { rows.map { it.session.toSessionRef() } }
    LaunchedEffect(refs) { selection.keepOnly(refs.map { it.id }) }
    NavigationBackHandler(
        state = rememberNavigationEventState(currentInfo = NavigationEventInfo.None),
        isBackEnabled = selection.active,
        onBackCompleted = { selection.clear() },
    )

    Scaffold(
        topBar = {
            SwitchingTopBar(
                selecting = selection.active,
                selectionBar = {
                    SelectionTopBar(
                        selection = selection,
                        all = refs,
                        onArchive = actions::archive,
                        onUnarchive = actions::unarchive,
                        onResume = actions::resume,
                    )
                },
            ) {
                TopAppBar(
                    navigationIcon = {
                        IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                    },
                    title = { Text("Archived") },
                )
            }
        },
        snackbarHost = { SnackbarHost(snackbar) },
    ) { padding ->
        PullToRefreshBox(
            isRefreshing = refreshing,
            onRefresh = {
                scope.launch {
                    refreshing = true
                    load()
                    refreshing = false
                }
            },
            modifier = Modifier.padding(padding.exceptBottom()).consumeWindowInsets(padding).fillMaxSize(),
        ) {
            Box(Modifier.fillMaxSize()) {
                LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = padding.calculateBottomPadding())) {
                    error?.let {
                        item(key = "error") {
                            Text(it, color = MaterialTheme.colorScheme.error, modifier = Modifier.animateItem().padding(16.dp))
                        }
                    }
                    if (sessions?.isEmpty() == true) {
                        item(key = "empty") {
                            Text(
                                "No archived sessions. Swipe a session sideways or long press it to archive.",
                                modifier = Modifier.animateItem().padding(16.dp),
                            )
                        }
                    }
                    items(rows, key = { it.session.id }) { s ->
                        SessionItem(
                            item = s,
                            selection = selection,
                            busy = actions.busy(s.session.id),
                            engaged = actions.engaged(s.session.id),
                            onOpen = onOpen,
                            onArchive = actions::archive,
                            onUnarchive = actions::unarchive,
                        )
                    }
                }
                if (sessions == null && error == null) {
                    CircularProgressIndicator(Modifier.align(Alignment.Center))
                }
            }
        }
    }
}
