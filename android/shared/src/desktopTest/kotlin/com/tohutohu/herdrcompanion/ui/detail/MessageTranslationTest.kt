package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import com.tohutohu.herdrcompanion.data.Message
import com.tohutohu.herdrcompanion.data.api.BlockDto
import org.junit.Rule
import org.junit.Test

class MessageTranslationTest {
    @get:Rule val rule = createComposeRule()

    private var block by mutableStateOf(BlockDto("text", text = "入力した日本語", originalText = "Sent English"))

    private fun content(role: String) {
        rule.setContent {
            MaterialTheme {
                MessageItem(
                    message = Message("message", role, 0, listOf(block)),
                    resolveUrl = { it }, formatFileSize = { "$it bytes" },
                    onOpenFile = { _, _ -> }, onOpenImage = {},
                    interactionsEnabled = true, onRespond = {},
                )
            }
        }
    }

    @Test
    fun 日本語の入力を既定で表示し送信した英語と往復できる() {
        content("user")
        rule.onNodeWithText("入力した日本語").assertExists()
        rule.onNodeWithText("Sent English").assertDoesNotExist()
        rule.onNodeWithText("送信した英語を表示 · PLaMo").performClick()
        rule.onNodeWithText("Sent English").assertExists()
        // A polling refresh with equal content must keep the selected view.
        rule.runOnIdle { block = block.copy() }
        rule.onNodeWithText("Sent English").assertExists()
        rule.onNodeWithText("入力した原文を表示").performClick()
        rule.onNodeWithText("入力した日本語").assertExists()
    }

    @Test
    fun 報告は日本語訳から原文へ切り替えられる() {
        content("assistant")
        rule.onNodeWithText("原文を表示 · PLaMo").performClick()
        rule.onNodeWithText("Sent English").assertExists()
        rule.onNodeWithText("日本語訳を表示").performClick()
        rule.onNodeWithText("入力した日本語").assertExists()
    }

    @Test
    fun 本文と同じ原文には翻訳の切替を表示しない() {
        block = BlockDto("text", text = "日本語のまま", originalText = "日本語のまま")
        content("assistant")
        rule.onNodeWithText("日本語のまま").assertExists()
        rule.onNodeWithText("原文を表示 · PLaMo").assertDoesNotExist()
    }
}
