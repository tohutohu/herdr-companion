package com.tohutohu.herdrcompanion.ui.newsession

import android.widget.Toast
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.platform.LocalContext
import com.tohutohu.herdrcompanion.container

/** Android route: owns launch state and Android side effects. */
@Composable
fun StartingSessionRoute(
    startId: String,
    onBack: () -> Unit,
    onReady: (String) -> Unit,
) {
    val context = LocalContext.current
    val container = context.container
    val starts = container.sessionStarts
    val entries by starts.entries.collectAsState()
    val entry = entries.find { it.id == startId }

    val sessions by container.sessions.collectAsState()
    LaunchedEffect(sessions, entry) {
        val rows = sessions.orEmpty()
        starts.reconcile(rows.map { it.id }.toSet())
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
                StartingSessionAction.Dismiss -> {
                    starts.dismiss(startId)
                    onBack()
                }
            }
        },
    )
}
