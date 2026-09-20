package com.tohutohu.herdrcompanion.ui.sessions

import com.tohutohu.herdrcompanion.model.PendingStartUiState
import com.tohutohu.herdrcompanion.model.SessionUiModel

/** Returns whether a session contains [query] in one of its useful fields. */
fun SessionUiModel.matchesSessionSearch(query: String): Boolean {
    val needle = query.trim().lowercase()
    if (needle.isEmpty()) return true
    return listOfNotNull(
        id,
        title,
        project,
        cwd,
        provider,
        providerName,
        model,
        effort,
        mode,
        lastMessage,
    ).any { it.lowercase().contains(needle) }
}

/** In-flight starts are searchable before the gateway session exists. */
fun PendingStartUiState.matchesSessionSearch(query: String): Boolean {
    val needle = query.trim().lowercase()
    if (needle.isEmpty()) return true
    return listOf(cwd, prompt, label, sessionId).filterNotNull().any { it.lowercase().contains(needle) }
}

fun List<SessionListItemUiState>.filterSessionSearch(query: String): List<SessionListItemUiState> =
    filter { it.session.matchesSessionSearch(query) }
