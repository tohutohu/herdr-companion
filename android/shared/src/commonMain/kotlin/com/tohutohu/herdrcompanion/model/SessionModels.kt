package com.tohutohu.herdrcompanion.model

import com.tohutohu.herdrcompanion.data.api.Backend

/** Presentation model shared by the Android route and the Desktop shell. */
data class SessionUiModel(
    val id: String,
    val provider: String,
    val providerName: String,
    val project: String,
    val title: String?,
    val cwd: String?,
    val status: String,
    val lastMessage: String?,
    val paneId: String?,
    val canSend: Boolean,
    val model: String?,
    val effort: String?,
    val mode: String?,
    val contextUsedTokens: Long?,
    val contextWindowTokens: Long?,
    val contextUsedPercent: Int?,
    val costUsd: Double?,
    val costEstimated: Boolean?,
    val archived: Boolean,
    val live: Boolean,
    /** Whether a notification arrived since the session was last opened. */
    val unread: Boolean = false,
    /** Where a live session runs ("direct" or "herdr"); null when offline or unknown. */
    val backend: String? = null,
    /** A requested model / effort the agent has not used yet. */
    val pendingModel: String? = null,
    val pendingEffort: String? = null,
    /** When the pending change takes effect: "next_request" or "next_turn". */
    val settingsApply: String? = null,
    /** Why a session the gateway runs directly failed. */
    val error: String? = null,
) {
    /** Run by the gateway itself, so its model and effort can be changed while it runs. */
    val direct: Boolean get() = live && backend == Backend.DIRECT
}

fun SessionUiModel.headline(): String =
    title?.takeIf { it.isNotBlank() } ?: (project.ifBlank { cwd ?: id }).substringAfterLast('/')

/** What the common list needs from an in-flight Android session start. */
data class PendingStartUiState(
    val id: String,
    val cwd: String,
    val prompt: String,
    val label: String,
    val busy: Boolean,
    val sessionId: String?,
)
