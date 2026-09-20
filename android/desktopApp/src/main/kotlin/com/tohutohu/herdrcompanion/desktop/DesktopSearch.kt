package com.tohutohu.herdrcompanion.desktop

import com.tohutohu.herdrcompanion.model.SessionUiModel
import com.tohutohu.herdrcompanion.ui.sessions.SessionListItemUiState
import com.tohutohu.herdrcompanion.ui.sessions.matchesSessionSearch

/** Local-only matching for the quick session switcher. No Gateway request is made. */
fun SessionUiModel.matchesDesktopSearch(query: String): Boolean {
    return matchesSessionSearch(query)
}

fun List<SessionListItemUiState>.filterDesktopSessions(query: String): List<SessionListItemUiState> =
    filter { it.session.matchesDesktopSearch(query) }

fun nextDesktopSearchIndex(current: Int, size: Int, delta: Int): Int {
    if (size <= 0) return 0
    return (current + delta).coerceIn(0, size - 1)
}
