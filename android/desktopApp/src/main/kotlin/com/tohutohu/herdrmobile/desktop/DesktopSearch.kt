package com.tohutohu.herdrmobile.desktop

import com.tohutohu.herdrmobile.model.SessionUiModel
import com.tohutohu.herdrmobile.ui.sessions.SessionListItemUiState

/** Local-only matching for the quick session switcher. No Gateway request is made. */
fun SessionUiModel.matchesDesktopSearch(query: String): Boolean {
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

fun List<SessionListItemUiState>.filterDesktopSessions(query: String): List<SessionListItemUiState> =
    filter { it.session.matchesDesktopSearch(query) }

fun nextDesktopSearchIndex(current: Int, size: Int, delta: Int): Int {
    if (size <= 0) return 0
    return (current + delta).coerceIn(0, size - 1)
}
