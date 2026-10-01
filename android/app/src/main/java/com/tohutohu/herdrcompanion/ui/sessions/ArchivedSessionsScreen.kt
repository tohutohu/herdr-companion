package com.tohutohu.herdrcompanion.ui.sessions

import android.text.format.DateUtils
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Search
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
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.container
import com.tohutohu.herdrcompanion.ui.changedInPlace
import com.tohutohu.herdrcompanion.ui.pausePlacementOnChange
import com.tohutohu.herdrcompanion.data.toEntity
import com.tohutohu.herdrcompanion.ui.toUiModel
import kotlinx.coroutines.launch
import kotlin.coroutines.cancellation.CancellationException
import androidx.navigationevent.NavigationEventInfo
import androidx.navigationevent.compose.NavigationBackHandler
import androidx.navigationevent.compose.rememberNavigationEventState
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.PaddingValues
import com.tohutohu.herdrcompanion.ui.exceptBottom

/**
 * Archived sessions, read directly from the gateway (not cached) one page at a
 * time as the list scrolls; searching loads the rest. Swipe a session sideways
 * to unarchive it, or long press to select several and unarchive or resume
 * from the selection bar.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ArchivedSessionsScreen(onBack: () -> Unit, onOpen: (String) -> Unit) {
    val repo = LocalContext.current.container.repository
    val scope = rememberCoroutineScope()
    val pager = remember(repo) { ArchivedSessionsPager { offset, limit -> repo.archivedSessions(offset, limit) } }
    val sessions = pager.sessions
    var error by remember { mutableStateOf<String?>(null) }
    var refreshing by remember { mutableStateOf(false) }
    var searchOpen by rememberSaveable { mutableStateOf(false) }
    var query by rememberSaveable { mutableStateOf("") }
    val searchFocusRequester = remember { FocusRequester() }
    val snackbar = remember { SnackbarHostState() }

    LaunchedEffect(searchOpen) {
        if (searchOpen) searchFocusRequester.requestFocus()
    }

    suspend fun load(keepLoaded: Boolean) {
        try {
            pager.load(keepLoaded)
            error = null
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            error = e.message ?: e.toString()
        }
    }

    suspend fun loadMore() {
        try {
            pager.loadMore()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            scope.launch { snackbar.showSnackbar("Could not load more: ${e.message ?: e}") }
        }
    }

    val actions = rememberSessionActions(snackbar = snackbar, onChanged = { load(keepLoaded = true) })
    actions.Dialogs()
    LaunchedEffect(Unit) { load(keepLoaded = false) }

    val allRows = remember(sessions) {
        val now = System.currentTimeMillis()
        sessions.orEmpty().map {
            val entity = it.toEntity(now, listed = false)
            SessionListItemUiState(entity.toUiModel(), DateUtils.getRelativeTimeSpanString(entity.updatedAt).toString())
        }
    }
    val rows = remember(allRows, query) { allRows.filter { it.session.matchesSessionSearch(query) } }
    val rowsResized = pausePlacementOnChange(rows) { a, b -> changedInPlace(a, b) { it.session.id } }
    val listState = rememberLazyListState()
    val nearEnd by remember {
        derivedStateOf {
            val info = listState.layoutInfo
            (info.visibleItemsInfo.lastOrNull()?.index ?: 0) >= info.totalItemsCount - 6
        }
    }
    val searching = query.isNotBlank()
    // A search covers every archived session, so it loads the remaining pages.
    LaunchedEffect(nearEnd, searching, pager.nextOffset) {
        if (nearEnd || searching) loadMore()
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
                if (searchOpen) {
                    SessionSearchTopBar(
                        query = query,
                        focusRequester = searchFocusRequester,
                        onQueryChange = { query = it },
                        onClose = {
                            query = ""
                            searchOpen = false
                        },
                    )
                } else {
                    TopAppBar(
                        navigationIcon = {
                            IconButton(onClick = onBack) {
                                Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                            }
                        },
                        title = { Text("Archived") },
                        actions = {
                            IconButton(onClick = { searchOpen = true }) {
                                Icon(Icons.Default.Search, contentDescription = "Search sessions")
                            }
                        },
                    )
                }
            }
        },
        snackbarHost = { SnackbarHost(snackbar) },
    ) { padding ->
        PullToRefreshBox(
            isRefreshing = refreshing,
            onRefresh = {
                scope.launch {
                    refreshing = true
                    load(keepLoaded = false)
                    refreshing = false
                }
            },
            modifier = Modifier.padding(padding.exceptBottom()).consumeWindowInsets(padding).fillMaxSize(),
        ) {
            Box(Modifier.fillMaxSize()) {
                LazyColumn(Modifier.fillMaxSize(), state = listState, contentPadding = PaddingValues(bottom = padding.calculateBottomPadding())) {
                    error?.let {
                        item(key = "error") {
                            Text(it, color = MaterialTheme.colorScheme.error, modifier = Modifier.animateItem().padding(16.dp))
                        }
                    }
                    if (sessions != null && rows.isEmpty() && !pager.hasMore) {
                        item(key = "empty") {
                            Text(
                                if (query.isBlank()) {
                                    "No archived sessions. Swipe a session sideways or long press it to archive."
                                } else {
                                    "No matching sessions."
                                },
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
                            pausePlacement = rowsResized,
                            onOpen = onOpen,
                            onArchive = actions::archive,
                            onUnarchive = actions::unarchive,
                        )
                    }
                    if (pager.hasMore) {
                        item(key = "more") {
                            Box(Modifier.animateItem().fillMaxWidth().padding(16.dp), contentAlignment = Alignment.Center) {
                                CircularProgressIndicator(Modifier.size(24.dp))
                            }
                        }
                    }
                }
                if (sessions == null && error == null) {
                    CircularProgressIndicator(Modifier.align(Alignment.Center))
                }
            }
        }
    }
}
