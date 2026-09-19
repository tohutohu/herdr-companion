package com.tohutohu.herdrmobile.ui.sessions

import com.tohutohu.herdrmobile.data.SessionStart
import com.tohutohu.herdrmobile.data.db.SessionEntity

/** Display data for one row. Formatting stays outside the common-ready UI. */
data class SessionListItemUiState(
    val session: SessionEntity,
    val relativeUpdatedAt: String,
)

/** The state needed to render the session list without accessing the app container. */
data class SessionListUiState(
    val sessions: List<SessionListItemUiState> = emptyList(),
    val pendingStarts: List<SessionStart> = emptyList(),
    val isLoaded: Boolean = false,
    val isRefreshing: Boolean = false,
    val error: String? = null,
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
    data class Archive(val session: SessionRef) : SessionListAction
    data class Unarchive(val session: SessionRef) : SessionListAction
    data class Resume(val session: SessionRef) : SessionListAction
}
