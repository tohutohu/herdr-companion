package com.tohutohu.herdrmobile.ui.settings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import com.tohutohu.herdrmobile.container
import com.tohutohu.herdrmobile.data.api.AgentUpdateDto
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

@Composable
fun AgentUpdatesSection() {
    val api = LocalContext.current.container.api
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val scope = rememberCoroutineScope()
    var agents by remember { mutableStateOf<List<AgentUpdateDto>>(emptyList()) }
    var error by remember { mutableStateOf<String?>(null) }
    var submitting by remember { mutableStateOf<String?>(null) }
    var expanded by remember { mutableStateOf<Set<String>>(emptySet()) }

    suspend fun refresh() {
        try {
            agents = api.agentUpdates().agents
            error = null
        } catch (e: CancellationException) { throw e
        } catch (e: Exception) { error = "Could not read agent updates: ${e.message}" }
    }
    LaunchedEffect(api, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            while (true) {
                refresh()
                delay(if (agents.any { it.state == "running" }) 2_000 else 10_000)
            }
        }
    }
    HorizontalDivider()
    Text("Agent updates on Mac", style = MaterialTheme.typography.titleMedium)
    Text("Check for updates and install them on the connected Mac. Updates continue if you leave this screen. Running sessions stay open.", style = MaterialTheme.typography.bodySmall)
    Text("Codex: an already-running shared server keeps its current version. After your sessions finish, restart that server on the Mac to use the updated version.", style = MaterialTheme.typography.bodySmall)
    error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
    agents.forEach { agent ->
        val running = agent.state == "running" || submitting == agent.provider
        Surface(tonalElevation = 1.dp, shape = MaterialTheme.shapes.medium) {
            Column(Modifier.fillMaxWidth().padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(if (agent.provider == "codex") "Codex" else "Claude Code", style = MaterialTheme.typography.titleSmall)
                Text(agent.version.ifBlank { "Version unavailable" })
                if (running) LinearProgressIndicator(Modifier.fillMaxWidth())
                if (agent.state == "succeeded") Text("Update check completed")
                if (agent.error.isNotBlank()) Text(agent.error, color = MaterialTheme.colorScheme.error)
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedButton(enabled = !running && submitting == null, onClick = {
                        submitting = agent.provider
                        scope.launch {
                            try {
                                val status = api.updateAgent(agent.provider)
                                agents = agents.map { if (it.provider == status.provider) status else it }
                                error = null
                            } catch (e: CancellationException) { throw e
                            } catch (e: Exception) {
                                error = "Could not confirm update request: ${e.message}. Refresh the status before retrying."
                            } finally { submitting = null }
                        }
                    }) { Text(if (running) "Updating…" else "Check and update") }
                    if (agent.output.isNotBlank()) TextButton(onClick = {
                        expanded = if (agent.provider in expanded) expanded - agent.provider else expanded + agent.provider
                    }) { Text("Output") }
                }
                if (agent.provider in expanded) Text(agent.output, style = MaterialTheme.typography.bodySmall)
            }
        }
    }
}
