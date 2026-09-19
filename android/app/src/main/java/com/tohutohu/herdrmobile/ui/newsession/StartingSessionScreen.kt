package com.tohutohu.herdrmobile.ui.newsession

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp

/** Pure Compose UI for the progress of a session start. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun StartingSessionScreen(
    state: StartingSessionUiState,
    onAction: (StartingSessionAction) -> Unit,
) {
    val entry = state.entry
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(entry?.request?.cwd?.substringAfterLast('/') ?: "Session start") },
                navigationIcon = { TextButton(onClick = { onAction(StartingSessionAction.Back) }) { Text("Back") } },
            )
        },
    ) { padding ->
        Column(
            Modifier
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(16.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            if (entry == null) {
                Text("Start status is no longer available. Check the session list before starting again.")
            } else {
                Text(providerName(entry.request.provider), style = MaterialTheme.typography.titleMedium)
                Text(entry.request.cwd)
                if (entry.request.prompt.isNotBlank()) {
                    Surface(
                        color = MaterialTheme.colorScheme.secondaryContainer,
                        shape = MaterialTheme.shapes.medium,
                    ) {
                        Text(entry.request.prompt, Modifier.padding(16.dp))
                    }
                }
                if (entry.busy) LinearProgressIndicator(Modifier.fillMaxWidth())
                Text(entry.label)
                entry.notice?.takeIf { it != entry.label }?.let { Text(it) }
                if (entry.trustPane != null && !entry.busy) {
                    Text("Trust this folder and allow the agent to access its files?")
                    Row {
                        TextButton(onClick = { onAction(StartingSessionAction.AnswerTrust(true)) }) {
                            Text("Trust")
                        }
                        TextButton(onClick = { onAction(StartingSessionAction.AnswerTrust(false)) }) {
                            Text("Cancel")
                        }
                    }
                } else if (!entry.busy && entry.sessionId == null) {
                    if (entry.needsContinue) {
                        entry.paneId?.let { pane ->
                            Text("Resolve the startup dialog in Terminal, then continue here. Your initial prompt is kept until the agent is ready.")
                            Row {
                                TextButton(onClick = { onAction(StartingSessionAction.OpenTerminal(pane)) }) {
                                    Text("Open terminal")
                                }
                                TextButton(onClick = { onAction(StartingSessionAction.Continue) }) {
                                    Text("Continue start")
                                }
                            }
                        }
                    } else {
                        Text("Codex is ready. Enter your first prompt in the terminal, and this session will appear in the list when its ID is available.")
                    }
                    TextButton(onClick = { onAction(StartingSessionAction.Dismiss) }) { Text("Dismiss") }
                }
            }
        }
    }
}
