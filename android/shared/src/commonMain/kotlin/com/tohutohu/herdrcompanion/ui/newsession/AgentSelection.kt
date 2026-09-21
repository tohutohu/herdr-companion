package com.tohutohu.herdrcompanion.ui.newsession

import com.tohutohu.herdrcompanion.data.AgentPreset
import com.tohutohu.herdrcompanion.data.api.EffortOptionDto
import com.tohutohu.herdrcompanion.data.api.ModelsResponse

/** A coding agent the gateway can start, with the names the app shows. */
data class ProviderInfo(val id: String, val name: String, val shortName: String)

val PROVIDERS = listOf(
    ProviderInfo("claude", "Claude Code", "Claude"),
    ProviderInfo("codex", "Codex", "Codex"),
    ProviderInfo("opencode", "OpenCode", "OpenCode"),
    ProviderInfo("devin", "Devin", "Devin"),
)

fun providerName(id: String): String = PROVIDERS.firstOrNull { it.id == id }?.name ?: id

/** Short enough to fit a chip next to a model name. */
fun providerShortName(id: String): String = PROVIDERS.firstOrNull { it.id == id }?.shortName ?: id

/**
 * Efforts offered for the picked model; the catalog's own list covers the
 * agent's default model and models that list none.
 */
fun effortsFor(catalog: ModelsResponse, modelId: String): List<EffortOptionDto> {
    val own = catalog.models.firstOrNull { it.id == modelId }?.efforts.orEmpty()
    return own.ifEmpty { catalog.efforts }
}

/**
 * The mode id to keep for [mode]: the agent's own mode is kept as "", so a
 * session started in it is never labelled with a mode.
 */
fun normalizedMode(catalog: ModelsResponse, mode: String): String =
    if (catalog.modes.firstOrNull { it.id == mode }?.default == true) "" else mode

/**
 * The picked combination, carrying the names [catalog] gives it. A model,
 * effort or mode the catalog does not know (it failed to load, or the agent
 * dropped the model) keeps its id as the only label it has.
 */
fun agentPreset(provider: String, model: String, effort: String, mode: String, catalog: ModelsResponse) = AgentPreset(
    provider = provider,
    model = model,
    effort = effort,
    mode = mode,
    modelName = catalog.models.firstOrNull { it.id == model }?.name.orEmpty(),
    effortName = effortsFor(catalog, model).firstOrNull { it.id == effort }?.name.orEmpty(),
    modeName = catalog.modes.firstOrNull { it.id == mode }?.name.orEmpty(),
)

/**
 * Chip label for a combination, e.g. "Claude Opus · High · Plan" or
 * "Codex default". The agent's own mode is left out: it is what a session
 * starts in anyway, and every favorite would carry the same word.
 */
fun presetLabel(preset: AgentPreset): String {
    val model = preset.modelName.ifEmpty { preset.model }
    val effort = preset.effortName.ifEmpty { preset.effort }
    val mode = preset.modeName.ifEmpty { preset.mode }
    val head = providerShortName(preset.provider) + if (model.isEmpty()) " default" else " $model"
    return listOf(head, effort, mode).filter { it.isNotEmpty() }.joinToString(" · ")
}

/** The stable label shown in a preset picker and in settings. */
fun presetTitle(preset: AgentPreset): String = preset.name.trim().ifEmpty { presetLabel(preset) }

/** The combination details shown below a user-provided preset name. */
fun presetDetails(preset: AgentPreset): String = presetLabel(preset)

/**
 * [current] with the names it is missing filled in from combinations that are
 * already named ([known] being the favorites and the last used one). That is
 * what labels a selection whose catalog has not loaded yet.
 */
fun withKnownNames(current: AgentPreset, known: List<AgentPreset>): AgentPreset {
    val match = known.firstOrNull { it.key == current.key } ?: return current
    return current.copy(
        modelName = current.modelName.ifEmpty { match.modelName },
        effortName = current.effortName.ifEmpty { match.effortName },
        modeName = current.modeName.ifEmpty { match.modeName },
        name = current.name.ifEmpty { match.name },
    )
}
