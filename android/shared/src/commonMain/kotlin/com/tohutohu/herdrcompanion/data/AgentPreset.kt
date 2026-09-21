package com.tohutohu.herdrcompanion.data

import kotlinx.serialization.Serializable

/** A saved coding-agent/model/effort/mode combination. */
@Serializable
data class AgentPreset(
    val provider: String,
    val model: String = "",
    val effort: String = "",
    /** Empty starts the session in the agent's own mode. */
    val mode: String = "",
    val modelName: String = "",
    val effortName: String = "",
    val modeName: String = "",
    /** User-facing name. Empty keeps the generated combination label. */
    val name: String = "",
) {
    val key get() = "$provider/$model/$effort/$mode"
}

@Serializable
data class AgentPresets(val presets: List<AgentPreset> = emptyList(), val lastUsed: AgentPreset? = null)

fun togglePreset(presets: List<AgentPreset>, preset: AgentPreset): List<AgentPreset> =
    if (presets.any { it.key == preset.key }) presets.filterNot { it.key == preset.key } else presets + preset

/** Inserts or replaces a preset while keeping the existing order. */
fun upsertPreset(presets: List<AgentPreset>, previousKey: String?, preset: AgentPreset): List<AgentPreset> {
    val next = presets.toMutableList()
    val target = previousKey?.let { key -> next.indexOfFirst { it.key == key } }?.takeIf { it >= 0 }
        ?: next.indexOfFirst { it.key == preset.key }.takeIf { it >= 0 }
    if (target != null) {
        next[target] = preset
        return next.withIndex()
            .filter { it.index == target || it.value.key != preset.key }
            .map { it.value }
    } else {
        next += preset
    }
    return next
}

/** Removes the preset with the same combination, regardless of its display name. */
fun removePreset(presets: List<AgentPreset>, preset: AgentPreset): List<AgentPreset> =
    presets.filterNot { it.key == preset.key }

fun movePreset(presets: List<AgentPreset>, fromIndex: Int, toIndex: Int): List<AgentPreset> {
    if (fromIndex !in presets.indices || toIndex !in presets.indices || fromIndex == toIndex) return presets
    return presets.toMutableList().apply { add(toIndex, removeAt(fromIndex)) }
}
