package com.tohutohu.herdrmobile.data

import android.content.Context
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json

/**
 * A favorite way to start an agent: the coding agent plus the model and the
 * reasoning effort to run it with. An empty [model] / [effort] keeps the
 * agent's own default.
 *
 * The names are kept as the provider's catalog spelled them when the preset
 * was saved, so a preset of the agent that is not selected can be labelled
 * without loading its catalog.
 */
@Serializable
data class AgentPreset(
    val provider: String,
    val model: String = "",
    val effort: String = "",
    val modelName: String = "",
    val effortName: String = "",
) {
    /** Identity of the combination; the names are display only. */
    val key get() = "$provider/$model/$effort"
}

/** Saved combinations plus the one the last session was started with. */
@Serializable
data class AgentPresets(val presets: List<AgentPreset> = emptyList(), val lastUsed: AgentPreset? = null)

/** Appends [preset], or drops it when the same combination is already saved. */
fun togglePreset(presets: List<AgentPreset>, preset: AgentPreset): List<AgentPreset> =
    if (presets.any { it.key == preset.key }) {
        presets.filterNot { it.key == preset.key }
    } else {
        presets + preset
    }

private val Context.agentPresetStore by preferencesDataStore("agent_presets")

class AgentPresetsStore(private val context: Context) {
    private val key = stringPreferencesKey("presets")
    private val json = Json { ignoreUnknownKeys = true }

    val presets: Flow<AgentPresets> = context.agentPresetStore.data.map { it.presets() }

    suspend fun toggle(preset: AgentPreset) = update { it.copy(presets = togglePreset(it.presets, preset)) }

    /** Remembered so that the next session starts from the same combination. */
    suspend fun recordUsed(preset: AgentPreset) = update { it.copy(lastUsed = preset) }

    private suspend fun update(f: (AgentPresets) -> AgentPresets) {
        context.agentPresetStore.edit { it[key] = json.encodeToString(f(it.presets())) }
    }

    // Unreadable content (an older format, a partial write) starts over empty.
    private fun Preferences.presets(): AgentPresets =
        this[key]?.let { runCatching { json.decodeFromString<AgentPresets>(it) }.getOrNull() } ?: AgentPresets()
}
