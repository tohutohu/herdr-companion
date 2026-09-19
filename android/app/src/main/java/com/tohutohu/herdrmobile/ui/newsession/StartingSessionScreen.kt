package com.tohutohu.herdrmobile.ui.newsession

import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrmobile.container
import kotlinx.coroutines.delay
import kotlinx.coroutines.CancellationException
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun StartingSessionScreen(startId: String, onBack: () -> Unit, onReady: (String) -> Unit, onOpenTerminal: (String) -> Unit) {
    val context = LocalContext.current
    val starts = context.container.sessionStarts
    val entries by starts.entries.collectAsState()
    val entry = entries.find { it.id == startId }
    LaunchedEffect(entry?.busy, entry?.trustPane, entry?.paneId) {
        if (entry == null || entry.busy || entry.trustPane != null || entry.paneId == null) return@LaunchedEffect
        while (true) {
            try { context.container.repository.refreshSessions() }
            catch (e: CancellationException) { throw e }
            catch (_: Exception) { }
            delay(5_000)
        }
    }
    val sessions by context.container.sessions.collectAsState()
    LaunchedEffect(sessions, entry) {
        val rows = sessions.orEmpty()
        starts.reconcile(rows.map { it.id }.toSet(), rows.mapNotNull { s -> s.paneId?.let { it to s.id } }.toMap())
    }
    LaunchedEffect(entry?.sessionId) {
        entry?.sessionId?.let { id ->
            entry.notice?.let { android.widget.Toast.makeText(context, it, android.widget.Toast.LENGTH_LONG).show() }
            onReady(id)
        }
    }
    Scaffold(topBar = {
        TopAppBar(title = { Text(entry?.request?.cwd?.substringAfterLast('/') ?: "Session start") },
            navigationIcon = { TextButton(onClick = onBack) { Text("Back") } })
    }) { padding ->
        Column(Modifier.padding(padding).verticalScroll(rememberScrollState()).padding(16.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
            if (entry == null) {
                Text("Start status is no longer available. Check the session list before starting again.")
            } else {
                Text(providerName(entry.request.provider), style = MaterialTheme.typography.titleMedium)
                Text(entry.request.cwd)
                if (entry.request.prompt.isNotBlank()) {
                    Surface(color = MaterialTheme.colorScheme.secondaryContainer, shape = MaterialTheme.shapes.medium) {
                        Text(entry.request.prompt, Modifier.padding(16.dp))
                    }
                }
                if (entry.busy) LinearProgressIndicator(Modifier.fillMaxWidth())
                Text(entry.label)
                entry.notice?.takeIf { it != entry.label }?.let { Text(it) }
                if (entry.trustPane != null && !entry.busy) {
                    Text("Trust this folder and allow the agent to access its files?")
                    Row {
                        TextButton(onClick = { starts.answerTrust(startId, true) }) { Text("Trust") }
                        TextButton(onClick = { starts.answerTrust(startId, false) }) { Text("Cancel") }
                    }
                } else if (!entry.busy && entry.sessionId == null) {
                    entry.paneId?.let { pane ->
                        Text("Resolve the startup dialog in Terminal, then continue here. Your initial prompt is kept until the agent is ready.")
                        Row {
                            TextButton(onClick = { onOpenTerminal(pane) }) { Text("Open terminal") }
                            TextButton(onClick = { starts.continueLaunch(startId) }) { Text("Continue start") }
                        }
                    }
                    TextButton(onClick = { starts.dismiss(startId); onBack() }) { Text("Dismiss") }
                }
            }
        }
    }
}
