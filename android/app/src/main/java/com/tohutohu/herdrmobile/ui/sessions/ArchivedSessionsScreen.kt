package com.tohutohu.herdrmobile.ui.sessions

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
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
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrmobile.container
import com.tohutohu.herdrmobile.data.api.SessionDto
import com.tohutohu.herdrmobile.data.toEntity
import kotlinx.coroutines.launch

/**
 * Archived sessions, read directly from the gateway (not cached). Long press
 * a session to unarchive or resume it.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ArchivedSessionsScreen(onBack: () -> Unit, onOpen: (String) -> Unit) {
    val repo = LocalContext.current.container.repository
    val scope = rememberCoroutineScope()
    var sessions by remember { mutableStateOf<List<SessionDto>?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var refreshing by remember { mutableStateOf(false) }

    suspend fun load() {
        try {
            sessions = repo.archivedSessions()
            error = null
        } catch (e: Exception) {
            error = e.message ?: e.toString()
        }
    }

    val actions = rememberSessionActions(onChanged = { load() })
    actions.Dialogs()
    LaunchedEffect(Unit) { load() }

    Scaffold(
        topBar = {
            TopAppBar(
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                title = { Text("Archived") },
            )
        },
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
            modifier = Modifier.padding(padding).fillMaxSize(),
        ) {
            LazyColumn(Modifier.fillMaxSize()) {
                error?.let {
                    item {
                        Text(it, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(16.dp))
                    }
                }
                val list = sessions
                if (list != null && list.isEmpty()) {
                    item { Text("No archived sessions. Long press a session to archive it.", modifier = Modifier.padding(16.dp)) }
                }
                items(list.orEmpty(), key = { it.id }) { s ->
                    val now = remember(s) { System.currentTimeMillis() }
                    SessionRow(s.toEntity(now, listed = false), actions, busy = actions.busyId == s.id, onClick = { onOpen(s.id) })
                    HorizontalDivider()
                }
            }
        }
    }
}
