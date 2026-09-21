package com.tohutohu.herdrcompanion.ui.settings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.container
import com.tohutohu.herdrcompanion.data.AgentPreset
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import com.tohutohu.herdrcompanion.ui.newsession.AgentPresetEditorDialog
import com.tohutohu.herdrcompanion.ui.newsession.presetDetails
import com.tohutohu.herdrcompanion.ui.newsession.presetTitle
import kotlinx.coroutines.launch

/** Settings for named agent/model/effort/mode combinations. */
@Composable
fun AgentPresetSettingsSection(
    api: GatewayApi,
    gatewayConfigured: Boolean,
) {
    val context = androidx.compose.ui.platform.LocalContext.current
    val store = context.container.agentPresets
    val stored by store.presets.collectAsState(initial = null)
    val presets = stored?.presets.orEmpty()
    val scope = rememberCoroutineScope()
    var editorVisible by remember { mutableStateOf(false) }
    var editing by remember { mutableStateOf<AgentPreset?>(null) }
    var deleteTarget by remember { mutableStateOf<AgentPreset?>(null) }

    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text("Session presets", style = MaterialTheme.typography.titleMedium)
        Text(
            "Save an agent, model, effort and mode once. New sessions can then use the combination from one dropdown.",
            style = MaterialTheme.typography.bodyMedium,
        )
        if (presets.isEmpty()) {
            Text("No presets configured yet.", style = MaterialTheme.typography.bodySmall)
        } else {
            presets.forEach { preset ->
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Column(Modifier.weight(1f).padding(vertical = 4.dp)) {
                        Text(
                            presetTitle(preset),
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                        if (preset.name.isNotBlank()) {
                            Text(
                                presetDetails(preset),
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                maxLines = 2,
                                overflow = TextOverflow.Ellipsis,
                            )
                        }
                    }
                    IconButton(onClick = {
                        editing = preset
                        editorVisible = true
                    }) {
                        Icon(Icons.Default.Edit, contentDescription = "Edit ${presetTitle(preset)}")
                    }
                    IconButton(onClick = { deleteTarget = preset }) {
                        Icon(Icons.Default.Delete, contentDescription = "Delete ${presetTitle(preset)}")
                    }
                }
            }
        }
        OutlinedButton(
            enabled = gatewayConfigured,
            onClick = {
                editing = null
                editorVisible = true
            },
        ) { Text("Add preset") }
        if (!gatewayConfigured) {
            Text(
                "Connect to a Gateway first to load the available agent and model options.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }

    if (editorVisible) {
        AgentPresetEditorDialog(
            initial = editing,
            loadModels = api::models,
            onSave = { next ->
                val previousKey = editing?.key
                editorVisible = false
                scope.launch { store.save(previousKey, next) }
            },
            onDismiss = { editorVisible = false },
        )
    }

    deleteTarget?.let { target ->
        AlertDialog(
            onDismissRequest = { deleteTarget = null },
            title = { Text("Delete preset?") },
            text = { Text("Remove ${presetTitle(target)} from your saved session presets?") },
            confirmButton = {
                TextButton(onClick = {
                    deleteTarget = null
                    scope.launch { store.delete(target) }
                }) { Text("Delete") }
            },
            dismissButton = { TextButton(onClick = { deleteTarget = null }) { Text("Cancel") } },
        )
    }
}
