package com.tohutohu.herdrcompanion.data

import android.content.Context
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map
import kotlinx.serialization.json.Json

private val Context.agentPresetStore by preferencesDataStore("agent_presets")

class AgentPresetsStore(private val context: Context) {
    private val key = stringPreferencesKey("presets")
    private val json = Json { ignoreUnknownKeys = true }

    val presets: Flow<AgentPresets> = context.agentPresetStore.data.map { it.presets() }

    suspend fun toggle(preset: AgentPreset) = update { it.copy(presets = togglePreset(it.presets, preset)) }

    suspend fun setOrder(presets: List<AgentPreset>) = update { it.copy(presets = presets) }

    /** Remembered so that the next session starts from the same combination. */
    suspend fun recordUsed(preset: AgentPreset) = update { it.copy(lastUsed = preset) }

    private suspend fun update(f: (AgentPresets) -> AgentPresets) {
        context.agentPresetStore.edit { it[key] = json.encodeToString(f(it.presets())) }
    }

    // Unreadable content (an older format, a partial write) starts over empty.
    private fun Preferences.presets(): AgentPresets =
        this[key]?.let { runCatching { json.decodeFromString<AgentPresets>(it) }.getOrNull() } ?: AgentPresets()
}
