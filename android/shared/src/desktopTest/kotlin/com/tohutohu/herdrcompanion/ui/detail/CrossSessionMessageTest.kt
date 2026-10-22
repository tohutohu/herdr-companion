package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.tohutohu.herdrcompanion.data.Message
import com.tohutohu.herdrcompanion.data.api.BlockDto
import kotlin.test.assertEquals
import org.junit.Rule
import org.junit.Test

class CrossSessionMessageTest {
    @get:Rule val rule = createComposeRule()

    private fun message(sessionId: String?) = Message(
        "peer-message", "system", 1_000,
        listOf(BlockDto("session_message", text = "確認しました。", senderName = "reviewer-a", sessionId = sessionId)),
    )

    @Test
    fun 別セッションの本文と送信元を表示しそのセッションを開ける() {
        var opened: SessionDetailAction? = null
        rule.setContent {
            MaterialTheme {
                SessionDetailScreen(
                    state = SessionDetailUiState("claude:current", null, listOf(message("claude:sender")), emptyList(), error = null, answering = false),
                    resolveUrl = { it }, formatFileSize = { "$it bytes" }, formatTime = { "$it" },
                    onAction = { opened = it },
                )
            }
        }
        rule.onNodeWithText("Message from reviewer-a").assertExists()
        rule.onNodeWithText("確認しました。").assertExists()
        rule.onNodeWithText("Open session").performClick()
        rule.runOnIdle { assertEquals(SessionDetailAction.OpenSession("claude:sender"), opened) }
    }

    @Test
    fun 送信元セッションを特定できなくても本文は表示する() {
        rule.setContent {
            MaterialTheme {
                MessageItem(
                    message = message(null), resolveUrl = { it }, formatFileSize = { "$it bytes" },
                    onOpenFile = { _, _ -> }, onOpenImage = {}, onOpenTerminal = {},
                    interactionsEnabled = true, onRespond = {},
                )
            }
        }
        rule.onNodeWithText("Message from reviewer-a").assertExists()
        rule.onNodeWithText("確認しました。").assertExists()
        rule.onNodeWithText("Open session").assertDoesNotExist()
    }
}
