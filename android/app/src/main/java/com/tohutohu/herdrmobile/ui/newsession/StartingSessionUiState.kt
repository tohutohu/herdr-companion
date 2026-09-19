package com.tohutohu.herdrmobile.ui.newsession

import com.tohutohu.herdrmobile.data.SessionStart

/** State needed to render the progress of a session start. */
data class StartingSessionUiState(
    val entry: SessionStart?,
)

/** User events emitted by the session-start screen. */
sealed interface StartingSessionAction {
    data object Back : StartingSessionAction
    data class AnswerTrust(val trusted: Boolean) : StartingSessionAction
    data class OpenTerminal(val paneId: String) : StartingSessionAction
    data object Continue : StartingSessionAction
    data object Dismiss : StartingSessionAction
}
