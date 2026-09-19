package com.tohutohu.herdrmobile.ui.sessions

import android.text.format.DateUtils
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import com.tohutohu.herdrmobile.container
import com.tohutohu.herdrmobile.ui.toUiModel
import com.tohutohu.herdrmobile.ui.toUiState
import com.tohutohu.herdrmobile.ui.usage.UsageCardRoute
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

private const val LIST_POLL_MS = 5_000L

/** Android route: owns the repository, lifecycle polling and session actions. */
@Composable
fun SessionListRoute(
    onOpen: (String) -> Unit,
    onSettings: () -> Unit,
    onNew: () -> Unit,
    onArchived: () -> Unit,
    onOpenStart: (String) -> Unit,
) {
    val context = LocalContext.current
    val container = context.container
    val repo = container.repository
    val loaded by container.sessions.collectAsState()
    val sessions = loaded.orEmpty()
    val starts by container.sessionStarts.entries.collectAsState()
    val pendingStarts = starts.filter { entry -> !entry.listed && sessions.none { it.id == entry.sessionId } }
    LaunchedEffect(sessions, starts) {
        container.sessionStarts.reconcile(
            sessions.map { it.id }.toSet(),
            sessions.mapNotNull { s -> s.paneId?.let { it to s.id } }.toMap(),
        )
    }
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

    val uiState = SessionListUiState(
        sessions = sessions.map { session ->
            SessionListItemUiState(
                session = session.toUiModel(),
                relativeUpdatedAt = DateUtils.getRelativeTimeSpanString(session.updatedAt).toString(),
            )
        },
        pendingStarts = pendingStarts.map { it.toUiState() },
        isLoaded = loaded != null,
        isRefreshing = refreshing,
        error = error,
        busySessionIds = actions.busyIds,
        engagedSessionIds = actions.engagedIds,
    )

    AndroidSessionListScreen(
        state = uiState,
        onAction = { action ->
            when (action) {
                is SessionListAction.OpenSession -> onOpen(action.sessionId)
                is SessionListAction.OpenStarting -> onOpenStart(action.startId)
                SessionListAction.OpenSettings -> onSettings()
                SessionListAction.OpenNewSession -> onNew()
                SessionListAction.OpenArchived -> onArchived()
                SessionListAction.Refresh -> scope.launch {
                    refreshing = true
                    refresh()
                    refreshing = false
                }
                is SessionListAction.Archive -> actions.archive(action.sessions)
                is SessionListAction.Unarchive -> actions.unarchive(action.sessions)
                is SessionListAction.Resume -> actions.resume(action.session)
            }
        },
        topContent = { UsageCardRoute() },
        snackbarHost = { SnackbarHost(snackbar) },
    )
}
