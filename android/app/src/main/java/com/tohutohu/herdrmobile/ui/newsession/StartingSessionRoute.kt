package com.tohutohu.herdrmobile.ui.newsession

import android.widget.Toast
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.platform.LocalContext
import com.tohutohu.herdrmobile.container
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay

/** Android route: owns launch state, refresh polling and Android side effects. */
@Composable
fun StartingSessionRoute(
    startId: String,
    onBack: () -> Unit,
    onReady: (String) -> Unit,
    onOpenTerminal: (String) -> Unit,
) {
    val context = LocalContext.current
    val container = context.container
    val starts = container.sessionStarts
    val entries by starts.entries.collectAsState()
    val entry = entries.find { it.id == startId }

    LaunchedEffect(entry?.busy, entry?.trustPane, entry?.paneId) {
        if (entry == null || entry.busy || entry.trustPane != null || entry.paneId == null) return@LaunchedEffect
        while (true) {
            try {
                container.repository.refreshSessions()
            } catch (e: CancellationException) {
                throw e
            } catch (_: Exception) {
                // The next poll can still discover the session.
            }
            delay(5_000)
        }
    }

    val sessions by container.sessions.collectAsState()
    LaunchedEffect(sessions, entry) {
        val rows = sessions.orEmpty()
        starts.reconcile(
            rows.map { it.id }.toSet(),
            rows.mapNotNull { session -> session.paneId?.let { it to session.id } }.toMap(),
        )
    }

    LaunchedEffect(entry?.sessionId) {
        entry?.sessionId?.let { id ->
            entry.notice?.let { Toast.makeText(context, it, Toast.LENGTH_LONG).show() }
            onReady(id)
        }
    }

    StartingSessionScreen(
        state = StartingSessionUiState(entry = entry),
        onAction = { action ->
            when (action) {
                StartingSessionAction.Back -> onBack()
                is StartingSessionAction.AnswerTrust -> starts.answerTrust(startId, action.trusted)
                is StartingSessionAction.OpenTerminal -> onOpenTerminal(action.paneId)
                StartingSessionAction.Continue -> starts.continueLaunch(startId)
                StartingSessionAction.Dismiss -> {
                    starts.dismiss(startId)
                    onBack()
                }
            }
        },
    )
}
