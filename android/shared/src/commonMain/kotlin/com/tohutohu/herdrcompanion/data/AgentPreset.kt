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
) {
    val key get() = "$provider/$model/$effort/$mode"
}

@Serializable
data class AgentPresets(val presets: List<AgentPreset> = emptyList(), val lastUsed: AgentPreset? = null)

fun togglePreset(presets: List<AgentPreset>, preset: AgentPreset): List<AgentPreset> =
    if (presets.any { it.key == preset.key }) presets.filterNot { it.key == preset.key } else presets + preset

fun movePreset(presets: List<AgentPreset>, fromIndex: Int, toIndex: Int): List<AgentPreset> {
    if (fromIndex !in presets.indices || toIndex !in presets.indices || fromIndex == toIndex) return presets
    return presets.toMutableList().apply { add(toIndex, removeAt(fromIndex)) }
}
