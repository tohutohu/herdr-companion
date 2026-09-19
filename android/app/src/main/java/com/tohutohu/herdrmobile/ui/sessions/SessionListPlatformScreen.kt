package com.tohutohu.herdrmobile.ui.sessions

import androidx.compose.runtime.Composable
import androidx.navigationevent.NavigationEventInfo
import androidx.navigationevent.compose.NavigationBackHandler
import androidx.navigationevent.compose.rememberNavigationEventState

/** Android shell for the common list; navigation-event remains platform-owned. */
@Composable
fun AndroidSessionListScreen(
    state: SessionListUiState,
    onAction: (SessionListAction) -> Unit,
    topContent: @Composable () -> Unit = {},
    snackbarHost: @Composable () -> Unit = {},
) {
    val selection = rememberSessionSelection()
    NavigationBackHandler(
        state = rememberNavigationEventState(currentInfo = NavigationEventInfo.None),
        isBackEnabled = selection.active,
        onBackCompleted = { selection.clear() },
    )
    SessionListScreen(
        state = state,
        onAction = onAction,
        topContent = topContent,
        snackbarHost = snackbarHost,
        selection = selection,
    )
}
