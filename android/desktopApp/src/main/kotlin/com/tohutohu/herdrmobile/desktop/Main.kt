package com.tohutohu.herdrmobile.desktop

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.VerticalDivider
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Window
import androidx.compose.ui.window.application
import androidx.compose.ui.window.rememberWindowState
import com.tohutohu.herdrmobile.data.api.GatewayApi
import com.tohutohu.herdrmobile.model.SessionUiModel
import com.tohutohu.herdrmobile.ui.detail.SessionDetailAction
import com.tohutohu.herdrmobile.ui.detail.SessionDetailScreen
import com.tohutohu.herdrmobile.ui.sessions.SessionListAction
import com.tohutohu.herdrmobile.ui.sessions.SessionListScreen
import com.tohutohu.herdrmobile.ui.sessions.SessionListUiState
import com.tohutohu.herdrmobile.ui.sessions.SessionRef
import com.tohutohu.herdrmobile.ui.theme.SharedTheme

fun main() = application {
    Window(
        onCloseRequest = ::exitApplication,
        title = "Herdr",
        state = rememberWindowState(width = 1280.dp, height = 800.dp),
    ) {
        window.minimumSize = java.awt.Dimension(980, 640)
        val connection = remember { DesktopConnectionConfig.load() }
        val http = remember { DesktopHttp.client() }
        val api = remember { GatewayApi(http) { connection.settings } }
        val appState = remember { DesktopAppState(connection, api, http) }
        DisposableEffect(appState) {
            onDispose { appState.close() }
        }
        SharedTheme { DesktopApp(appState, api) }
    }
}

@Composable
private fun DesktopApp(
    state: DesktopAppState,
    api: GatewayApi,
) {
    Column(Modifier.fillMaxSize()) {
        Row(
            Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 14.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text("Herdr", style = MaterialTheme.typography.headlineSmall)
            Text(
                "  ${state.connectionLabel} · ${state.connection.baseUrl}",
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.weight(1f))
            TextButton(onClick = { state.refreshSessions(userInitiated = true) }) { Text("Refresh") }
            Button(onClick = state::openNewSession) { Text("+ New Session") }
        }
        state.transientError?.let { message ->
            Row(
                Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(message, color = MaterialTheme.colorScheme.error, modifier = Modifier.weight(1f))
                TextButton(onClick = { state.dismissError(); state.refreshSessions(userInitiated = true) }) { Text("Retry") }
            }
        }
        HorizontalDivider()
        Row(Modifier.fillMaxSize()) {
            Column(
                Modifier.width(360.dp).widthIn(min = 280.dp, max = 420.dp).fillMaxHeight(),
            ) {
                Text(
                    "Sessions",
                    style = MaterialTheme.typography.titleMedium,
                    modifier = Modifier.padding(horizontal = 16.dp, vertical = 12.dp),
                )
                SessionListScreen(
                    state = SessionListUiState(
                        sessions = state.sessions,
                        isLoaded = state.listLoaded,
                        isRefreshing = state.listRefreshing,
                        error = state.listError,
                        busySessionIds = state.busySessionIds,
                    ),
                    showTopBar = false,
                    showNewSessionFab = false,
                    onAction = { action -> handleListAction(state, action) },
                )
            }
            VerticalDivider()
            Box(Modifier.weight(1f).fillMaxHeight()) {
                val detail = state.detail
                if (detail == null) {
                    EmptyDetail(state)
                } else {
                    SessionDetailScreen(
                        state = detail,
                        resolveUrl = api::absolute,
                        formatFileSize = ::formatFileSize,
                        onAction = { action -> handleDetailAction(state, action) },
                    )
                }
            }
        }
    }

    if (state.newSessionOpen) {
        DesktopNewSessionWindow(
            api = api,
            onCreated = { id ->
                state.closeNewSession()
                state.refreshSessions(userInitiated = true)
                if (id != null) state.selectSession(id)
            },
            onDismiss = state::closeNewSession,
            onError = state::reportError,
        )
    }

    state.archiveConfirmation?.let {
        androidx.compose.material3.AlertDialog(
            onDismissRequest = state::cancelArchive,
            title = { Text("Stop and archive this session?") },
            text = { Text("The running agent pane will be closed. You can resume the session later.") },
            confirmButton = { TextButton(onClick = state::confirmArchive) { Text("Stop and archive") } },
            dismissButton = { TextButton(onClick = state::cancelArchive) { Text("Cancel") } },
        )
    }
}

@Composable
private fun EmptyDetail(state: DesktopAppState) {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        if (!state.listLoaded && state.listError == null) {
            CircularProgressIndicator()
        } else {
            Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text("Select a session", style = MaterialTheme.typography.titleLarge)
                if (state.listError != null) {
                    Text(state.listError!!, color = MaterialTheme.colorScheme.error)
                    TextButton(onClick = { state.refreshSessions(userInitiated = true) }) { Text("Retry") }
                }
            }
        }
    }
}

private fun handleListAction(state: DesktopAppState, action: SessionListAction) {
    when (action) {
        is SessionListAction.OpenSession -> state.selectSession(action.sessionId)
        is SessionListAction.Archive -> action.sessions.firstOrNull()?.let(state::requestArchive)
        is SessionListAction.Unarchive -> action.sessions.firstOrNull()?.let(state::unarchive)
        is SessionListAction.Resume -> state.resume(action.session)
        SessionListAction.OpenNewSession -> state.openNewSession()
        SessionListAction.Refresh -> state.refreshSessions(userInitiated = true)
        SessionListAction.OpenArchived -> state.reportError("Archived session browsing is not yet available on Desktop.")
        SessionListAction.OpenSettings -> state.reportError("Desktop uses the macOS Gateway config at ~/.config/herdr-mobile/desktop/config.json.")
        is SessionListAction.OpenStarting -> state.reportError("Session startup is handled in the New Session window.")
    }
}

private fun handleDetailAction(state: DesktopAppState, action: SessionDetailAction) {
    when (action) {
        SessionDetailAction.Back -> state.clearSelection()
        is SessionDetailAction.Send -> state.send(action.text)
        is SessionDetailAction.Retry -> state.retry(action.localId)
        is SessionDetailAction.Discard -> state.discard(action.localId)
        is SessionDetailAction.Respond -> state.respond(action.response)
        SessionDetailAction.Archive -> state.detail?.session?.let { state.requestArchive(it.toRef()) }
        SessionDetailAction.Unarchive -> state.detail?.session?.let { state.unarchive(it.toRef()) }
        SessionDetailAction.Resume -> state.detail?.session?.let { state.resume(it.toRef()) }
        SessionDetailAction.PickImage,
        SessionDetailAction.PickFile -> state.reportError("Attachments are not available in the Desktop client yet.")
        is SessionDetailAction.RemoveAttachment -> Unit
        is SessionDetailAction.OpenFile -> state.reportError("Opening Gateway files is not available in the Desktop client yet.")
        is SessionDetailAction.OpenImage -> state.reportError("Opening Gateway images is not available in the Desktop client yet.")
        SessionDetailAction.OpenTerminal -> state.reportError("Terminal view is not available in the Desktop client yet.")
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
