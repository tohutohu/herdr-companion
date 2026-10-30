package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.api.CatalogOption
import com.tohutohu.herdrcompanion.data.api.ModelsResponse
import com.tohutohu.herdrcompanion.data.api.SettingsApply
import com.tohutohu.herdrcompanion.model.SessionUiModel
import com.tohutohu.herdrcompanion.ui.newsession.OptionPicker
import com.tohutohu.herdrcompanion.ui.newsession.effortsFor

/** Agents whose sessions can be reconfigured while they run. */
private val CONFIGURABLE_PROVIDERS = setOf("claude", "codex")

/** Whether the details panel offers model and effort pickers for [session]. */
fun settingsChangeable(session: SessionUiModel): Boolean =
    session.live && session.provider in CONFIGURABLE_PROVIDERS

/**
 * A change the agent has not picked up yet, e.g. "→ Sonnet, from the next
 * turn". Claude takes a new model with its next model request, which can
 * be inside the turn that is running; Codex waits for the next turn.
 */
fun pendingSettingLabel(name: String, apply: String?): String = when (apply) {
    SettingsApply.NEXT_REQUEST -> "→ $name, applies from the next model request"
    SettingsApply.NEXT_TURN -> "→ $name, applies from the next turn"
    else -> "→ $name, not applied yet"
}

/** The catalog id of the mode the session reports by its display label. */
fun currentModeId(catalog: ModelsResponse, mode: String?): String {
    if (mode.isNullOrBlank()) return catalog.modes.firstOrNull { it.default }?.id.orEmpty()
    return catalog.modes.firstOrNull { it.id == mode || it.name.equals(mode, ignoreCase = true) }?.id ?: mode
}

private fun nameOf(options: List<CatalogOption>, id: String) = options.firstOrNull { it.id == id }?.name ?: id

/**
 * Model, effort and mode of a running session.
 * The pickers show what the agent uses now; a requested change is listed
 * under them until the agent reports it. A refused change is shown as the
 * gateway worded it: nothing falls back to another model.
 */
@Composable
internal fun SessionSettingsPanel(
    session: SessionUiModel,
    settings: SessionSettingsUiState,
    onAction: (SessionDetailAction) -> Unit,
) {
    val catalog = settings.catalog
    Column(
        Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        when {
            catalog == null && settings.loadError != null -> Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    "Could not load models: ${settings.loadError}",
                    color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.weight(1f),
                )
                TextButton(onClick = { onAction(SessionDetailAction.LoadSettings) }) { Text("Retry") }
            }
            catalog == null -> Text(
                "Loading models…",
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                style = MaterialTheme.typography.bodySmall,
            )
            else -> {
                val enabled = !settings.changing
                val model = session.model.orEmpty()
                OptionPicker(
                    label = "Model",
                    options = catalog.models,
                    selected = model,
                    error = null,
                    enabled = enabled,
                    onSelect = { if (it != (session.pendingModel ?: model)) onAction(SessionDetailAction.SetModel(it)) },
                    modifier = Modifier.fillMaxWidth(),
                    defaultEntry = false,
                )
                session.pendingModel?.let { PendingSetting(nameOf(catalog.models, it), session.settingsApply) }
                // Efforts follow the model the next request will use.
                val efforts = effortsFor(catalog, session.pendingModel ?: model)
                if (efforts.isNotEmpty()) {
                    val effort = session.effort.orEmpty()
                    OptionPicker(
                        label = "Effort",
                        options = efforts,
                        selected = effort,
                        error = null,
                        enabled = enabled,
                        onSelect = { if (it != (session.pendingEffort ?: effort)) onAction(SessionDetailAction.SetEffort(it)) },
                        modifier = Modifier.fillMaxWidth(),
                        defaultEntry = false,
                    )
                    session.pendingEffort?.let { PendingSetting(nameOf(efforts, it), session.settingsApply) }
                }
                if (catalog.modes.isNotEmpty()) {
                    val mode = currentModeId(catalog, session.mode)
                    OptionPicker(
                        label = "Mode",
                        options = catalog.modes,
                        selected = mode,
                        error = null,
                        enabled = enabled,
                        onSelect = { if (it != mode) onAction(SessionDetailAction.SetMode(it)) },
                        modifier = Modifier.fillMaxWidth(),
                        defaultEntry = false,
                    )
                }
            }
        }
        settings.changeError?.let {
            Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
        }
    }
}

@Composable
private fun PendingSetting(name: String, apply: String?) {
    Text(
        pendingSettingLabel(name, apply),
        color = MaterialTheme.colorScheme.tertiary,
        style = MaterialTheme.typography.bodySmall,
        modifier = Modifier.padding(start = 4.dp),
    )
}
