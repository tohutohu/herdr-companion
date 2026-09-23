package com.tohutohu.herdrcompanion.ui.newsession

import com.tohutohu.herdrcompanion.data.AgentPreset
import com.tohutohu.herdrcompanion.data.DirectoryShortcuts
import com.tohutohu.herdrcompanion.data.api.DirListingDto
import com.tohutohu.herdrcompanion.data.api.ModelsResponse
import com.tohutohu.herdrcompanion.data.api.StartSessionRequest

/** State needed to render the new-session form and directory browser. */
data class NewSessionUiState(
    val provider: String,
    val path: String,
    val listing: DirListingDto?,
    val loading: Boolean,
    val error: String?,
    val prompt: String,
    val starting: Boolean,
    val checking: Boolean,
    val pendingStart: PendingNewSessionStart?,
    val showMkdir: Boolean,
    val showPicker: Boolean,
    val model: String,
    val effort: String,
    val mode: String,
    val catalog: ModelsResponse,
    val modelsLoading: Boolean,
    val modelsError: String?,
    val shortcuts: DirectoryShortcuts,
    val savedPresets: List<AgentPreset>,
    val currentPreset: AgentPreset,
    val favorite: Boolean,
)

data class PendingNewSessionStart(
    val request: StartSessionRequest,
    val preset: AgentPreset,
)

/** User events emitted by the new-session form. */
sealed interface NewSessionAction {
    data object Back : NewSessionAction
    data class SetProvider(val provider: String) : NewSessionAction
    data class SetModel(val model: String) : NewSessionAction
    data class SetEffort(val effort: String) : NewSessionAction
    data class SetMode(val mode: String) : NewSessionAction
    data class SetPrompt(val prompt: String) : NewSessionAction
    data class SelectPreset(val preset: AgentPreset) : NewSessionAction
    data class ReorderPresets(val presets: List<AgentPreset>) : NewSessionAction
    data class ReorderFavoriteDirectories(val paths: List<String>) : NewSessionAction
    data object CustomizeAgent : NewSessionAction
    data object ToggleFavorite : NewSessionAction
    data object ToggleFavoriteDirectory : NewSessionAction
    data class OpenDirectory(val path: String) : NewSessionAction
    data object OpenParent : NewSessionAction
    data object ShowMkdir : NewSessionAction
    data object CancelMkdir : NewSessionAction
    data class CreateDirectory(val name: String) : NewSessionAction
    data object DismissPicker : NewSessionAction
    data object StartAnyway : NewSessionAction
    data object ChangeFolder : NewSessionAction
    data object Start : NewSessionAction
    /** Starts the session and opens it beside the current ones (Desktop only). */
    data object StartInSplit : NewSessionAction
}
