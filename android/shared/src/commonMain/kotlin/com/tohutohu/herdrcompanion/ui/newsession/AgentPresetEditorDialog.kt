package com.tohutohu.herdrcompanion.ui.newsession

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.AgentPreset
import com.tohutohu.herdrcompanion.data.api.ModelsResponse
import kotlinx.coroutines.CancellationException

/**
 * Editor shared by Android Settings and the Compose Desktop Settings dialog.
 * The caller owns persistence; this dialog only loads catalogs and returns a
 * complete, named combination.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AgentPresetEditorDialog(
    initial: AgentPreset?,
    loadModels: suspend (String) -> ModelsResponse,
    onSave: (AgentPreset) -> Unit,
    onDismiss: () -> Unit,
) {
    var name by remember(initial) { mutableStateOf(initial?.name.orEmpty()) }
    var provider by remember(initial) { mutableStateOf(initial?.provider ?: "claude") }
    var model by remember(initial) { mutableStateOf(initial?.model.orEmpty()) }
    var effort by remember(initial) { mutableStateOf(initial?.effort.orEmpty()) }
    var mode by remember(initial) { mutableStateOf(initial?.mode.orEmpty()) }
    var catalog by remember { mutableStateOf(ModelsResponse()) }
    var modelsLoading by remember { mutableStateOf(false) }
    var modelsError by remember { mutableStateOf<String?>(null) }

    LaunchedEffect(provider) {
        modelsLoading = true
        modelsError = null
        try {
            catalog = loadModels(provider)
        } catch (cause: CancellationException) {
            throw cause
        } catch (cause: Exception) {
            modelsError = cause.message ?: "Unknown error"
        } finally {
            modelsLoading = false
        }
    }

    val combination = withKnownNames(
        agentPreset(provider, model, effort, mode, catalog),
        listOfNotNull(initial),
    )
    val canSave = name.trim().isNotEmpty() && !modelsLoading
    val efforts = effortsFor(catalog, model)

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(if (initial == null) "New session preset" else "Edit session preset") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    label = { Text("Preset name") },
                    placeholder = { Text("e.g. Review with Claude") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                    PROVIDERS.forEachIndexed { index, info ->
                        SegmentedButton(
                            selected = provider == info.id,
                            onClick = {
                                if (provider != info.id) {
                                    provider = info.id
                                    model = ""
                                    effort = ""
                                    mode = ""
                                }
                            },
                            shape = SegmentedButtonDefaults.itemShape(index, PROVIDERS.size),
                        ) { Text(info.shortName) }
                    }
                }
                OptionPicker(
                    label = "Model",
                    options = catalog.models,
                    selected = model,
                    error = modelsError,
                    enabled = !modelsLoading && modelsError == null,
                    onSelect = {
                        model = it
                        if (effortsFor(catalog, it).none { effortOption -> effortOption.id == effort }) effort = ""
                    },
                    modifier = Modifier.fillMaxWidth(),
                )
                if (efforts.isNotEmpty()) {
                    OptionPicker(
                        label = "Effort",
                        options = efforts,
                        selected = effort,
                        error = null,
                        enabled = !modelsLoading && modelsError == null,
                        onSelect = { effort = it },
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
                if (catalog.modes.isNotEmpty()) {
                    OptionPicker(
                        label = "Mode",
                        options = catalog.modes,
                        selected = mode,
                        error = null,
                        enabled = !modelsLoading && modelsError == null,
                        onSelect = { mode = normalizedMode(catalog, it) },
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
                if (modelsLoading) {
                    Text("Loading available models…", style = androidx.compose.material3.MaterialTheme.typography.bodySmall)
                }
                if (modelsError != null) {
                    Text(
                        "Could not load the catalog. You can still save the current values.",
                        style = androidx.compose.material3.MaterialTheme.typography.bodySmall,
                    )
                }
            }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
        confirmButton = {
            TextButton(
                enabled = canSave,
                onClick = { onSave(combination.copy(name = name.trim())) },
            ) { Text("Save") }
        },
    )
}
