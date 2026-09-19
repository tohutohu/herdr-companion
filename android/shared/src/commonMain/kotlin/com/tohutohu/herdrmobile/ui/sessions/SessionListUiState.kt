package com.tohutohu.herdrmobile.ui.sessions

import com.tohutohu.herdrmobile.model.PendingStartUiState
import com.tohutohu.herdrmobile.model.SessionUiModel

/** Display data for one row. Formatting stays outside the common-ready UI. */
data class SessionListItemUiState(
    val session: SessionUiModel,
    val relativeUpdatedAt: String,
    val selected: Boolean = false,
)

/** The state needed to render the session list without accessing the app container. */
data class SessionListUiState(
    val sessions: List<SessionListItemUiState> = emptyList(),
    val pendingStarts: List<PendingStartUiState> = emptyList(),
    val isLoaded: Boolean = false,
    val isRefreshing: Boolean = false,
    val error: String? = null,
    val emptyMessage: String = "No sessions. Start Claude Code or Codex inside Herdr on your Mac.",
    val busySessionIds: Set<String> = emptySet(),
    val engagedSessionIds: Set<String> = emptySet(),
)

/** User events emitted by the session-list UI. */
sealed interface SessionListAction {
    data class OpenSession(val sessionId: String) : SessionListAction
    data class OpenStarting(val startId: String) : SessionListAction
    data object OpenSettings : SessionListAction
    data object OpenNewSession : SessionListAction
    data object OpenArchived : SessionListAction
    data object Refresh : SessionListAction
    data class Archive(val sessions: List<SessionRef>) : SessionListAction
    data class Unarchive(val sessions: List<SessionRef>) : SessionListAction
    data class Resume(val session: SessionRef) : SessionListAction
}
