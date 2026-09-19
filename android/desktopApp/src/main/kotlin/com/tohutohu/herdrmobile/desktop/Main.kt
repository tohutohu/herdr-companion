package com.tohutohu.herdrmobile.desktop

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.width
import androidx.compose.material3.VerticalDivider
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Window
import androidx.compose.ui.window.application
import com.tohutohu.herdrmobile.data.Message
import com.tohutohu.herdrmobile.data.api.BlockDto
import com.tohutohu.herdrmobile.model.SessionUiModel
import com.tohutohu.herdrmobile.ui.detail.SessionDetailAction
import com.tohutohu.herdrmobile.ui.detail.SessionDetailScreen
import com.tohutohu.herdrmobile.ui.detail.SessionDetailUiState
import com.tohutohu.herdrmobile.ui.sessions.SessionListAction
import com.tohutohu.herdrmobile.ui.sessions.SessionListItemUiState
import com.tohutohu.herdrmobile.ui.sessions.SessionListScreen
import com.tohutohu.herdrmobile.ui.sessions.SessionListUiState
import com.tohutohu.herdrmobile.ui.theme.SharedTheme

fun main() = application {
    Window(
        onCloseRequest = ::exitApplication,
        title = "Herdr",
    ) {
        SharedTheme {
            DesktopApp()
        }
    }
}

@Composable
private fun DesktopApp() {
    val session = rememberDemoSession()
    val messages = rememberDemoMessages()
    Row(Modifier.fillMaxSize()) {
        Box(Modifier.width(360.dp).fillMaxHeight()) {
            SessionListScreen(
                state = SessionListUiState(
                    sessions = listOf(SessionListItemUiState(session, "just now")),
                    isLoaded = true,
                ),
                onAction = { _: SessionListAction -> },
            )
        }
        VerticalDivider()
        Box(Modifier.weight(1f).fillMaxHeight()) {
            SessionDetailScreen(
                state = SessionDetailUiState(
                    sessionId = session.id,
                    session = session,
                    messages = messages,
                    pending = emptyList(),
                    error = null,
                    answering = false,
                ),
                resolveUrl = { it },
                formatFileSize = { "$it B" },
                onAction = { _: SessionDetailAction -> },
            )
        }
    }
}

private fun rememberDemoSession() = SessionUiModel(
    id = "desktop-demo",
    provider = "claude",
    providerName = "Claude",
    project = "herdr-android-client",
    title = "Shared Compose UI",
    cwd = "/workspace/herdr-android-client",
    status = "running",
    lastMessage = "Rendered from commonMain",
    paneId = "desktop-demo-pane",
    canSend = true,
    model = "Sonnet",
    effort = "medium",
    mode = "default",
    contextUsedTokens = 12_000,
    contextWindowTokens = 100_000,
    contextUsedPercent = 12,
    costUsd = 0.02,
    costEstimated = true,
    archived = false,
    live = true,
)

private fun rememberDemoMessages() = listOf(
    Message(
        id = "desktop-demo-user",
        role = "user",
        timestamp = 0L,
        blocks = listOf(BlockDto(type = "text", text = "Show the shared UI.")),
    ),
    Message(
        id = "desktop-demo-assistant",
        role = "assistant",
        timestamp = 1L,
        blocks = listOf(
            BlockDto(
                type = "text",
                text = "This SessionDetailScreen is rendered from commonMain on Desktop JVM.",
            ),
        ),
    ),
)
