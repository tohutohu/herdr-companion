package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.tohutohu.herdrcompanion.data.SendState
import kotlin.test.assertEquals
import org.junit.Rule
import org.junit.Test

class PendingMessageViewsTest {
    @get:Rule val rule = createComposeRule()

    @Test
    fun 送信成否が不明な本文を保持し未送信と断定せずRetryを無効にする() {
        var retries = 0
        var discards = 0
        val error = "Delivery unconfirmed. Check the conversation before sending again."
        rule.setContent {
            MaterialTheme {
                PendingMessageItem(
                    message = PendingMessageUiState("request", "original prompt", emptyList(), SendState.FAILED, error, canRetry = false),
                    resolveAttachmentPreview = { null },
                    onRetry = { retries++ },
                    onDiscard = { discards++ },
                )
            }
        }
        rule.onNodeWithText("original prompt").assertExists()
        rule.onNodeWithText(error).assertExists()
        rule.onNodeWithText("Not sent:", substring = true).assertDoesNotExist()
        rule.onNodeWithText("Retry").assertIsNotEnabled().performClick()
        rule.runOnIdle { assertEquals(0, retries) }
        rule.onNodeWithText("Discard").performClick()
        rule.runOnIdle { assertEquals(1, discards) }
    }

    @Test
    fun 安全に再送できる失敗ではRetryを実行できる() {
        var retries = 0
        rule.setContent {
            MaterialTheme {
                PendingMessageItem(
                    message = PendingMessageUiState("request", "original prompt", emptyList(), SendState.FAILED, "offline"),
                    resolveAttachmentPreview = { null },
                    onRetry = { retries++ },
                    onDiscard = {},
                )
            }
        }
        rule.onNodeWithText("Retry").assertIsEnabled().performClick()
        rule.runOnIdle { assertEquals(1, retries) }
    }
}
