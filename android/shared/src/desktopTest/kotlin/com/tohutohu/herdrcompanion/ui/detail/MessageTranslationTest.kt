package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.LocalSaveableStateRegistry
import androidx.compose.runtime.saveable.SaveableStateRegistry
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.longClick
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.Message
import com.tohutohu.herdrcompanion.data.api.BlockDto
import com.tohutohu.herdrcompanion.data.api.SessionDto
import com.tohutohu.herdrcompanion.model.toUiModel
import org.junit.Rule
import org.junit.Test

class MessageTranslationTest {
    @get:Rule val rule = createComposeRule()

    @Test
    fun 本文の長押しは操作メニューと競合せず返信単位で翻訳し原文へ戻せる() {
        val requests = mutableListOf<String>()
        rule.setContent {
            MaterialTheme {
                SessionDetailScreen(
                    state = SessionDetailUiState(
                        "session", SessionDto("session", "claude", status = "idle", updatedAt = "", translation = false).toUiModel(),
                        listOf(Message("a", "assistant", 0, listOf(BlockDto("text", text = "English report")))),
                        emptyList(), error = null, answering = false,
                    ),
                    resolveUrl = { it }, formatFileSize = { "$it bytes" }, formatTime = { "$it" },
                    translateText = { requests += it; "日本語の報告" }, onAction = {},
                )
            }
        }
        val report = rule.onNodeWithText("English report").fetchSemanticsNode().boundsInRoot
        val menu = rule.onNodeWithContentDescription("メッセージの操作").fetchSemanticsNode().boundsInRoot
        check(menu.left >= report.right) { "Message actions must sit beside the text" }
        check(menu.top < report.bottom) { "Message actions must not add a row below the reply" }
        rule.onNodeWithText("English report").performTouchInput { longClick() }
        rule.onNodeWithText("日本語に翻訳").assertDoesNotExist()
        rule.onNodeWithContentDescription("メッセージの操作").performClick()
        rule.onNodeWithText("日本語に翻訳").performClick()
        rule.onNodeWithText("日本語の報告").assertExists()
        rule.runOnIdle { check(requests == listOf("English report")) }
        rule.onNodeWithText("原文を表示").performClick()
        rule.onNodeWithText("日本語訳を表示").performClick()
        rule.onNodeWithText("日本語の報告").assertExists()
        rule.onNodeWithText("閉じる").performClick()
        rule.onNodeWithText("English report").assertExists()
    }

    @Test
    fun 手動翻訳の停止エラーから再試行できる() {
        var attempts = 0
        rule.setContent {
            MaterialTheme {
                CompositionLocalProvider(LocalMessageTranslator provides { _: String ->
                    if (attempts++ == 0) error("Gatewayメニューから翻訳を起動してください")
                    "翻訳できました"
                }) {
                    MessageTranslationMenu(Message("a", "assistant", 0, listOf(BlockDto("text", text = "Report"))))
                }
            }
        }
        rule.onNodeWithContentDescription("メッセージの操作").performClick()
        rule.onNodeWithText("日本語に翻訳").performClick()
        rule.onNodeWithText("Gatewayメニューから翻訳を起動してください").assertExists()
        rule.onNodeWithText("再試行").performClick()
        rule.onNodeWithText("翻訳できました").assertExists()
    }

    @Test
    fun メニューから会話全体とタイトルを切り替え再生成と新着でも原文表示を保つ() {
        var visible by mutableStateOf(true)
        var registry by mutableStateOf(SaveableStateRegistry(null) { true })
        var messages by mutableStateOf(listOf(
            Message("u", "user", 0, listOf(BlockDto("text", text = "入力した日本語", originalText = "Sent English"))),
            Message("a", "assistant", 1, listOf(BlockDto("text", text = "日本語の返信", originalText = "Original reply"))),
        ))
        rule.setContent {
            MaterialTheme {
                if (visible) CompositionLocalProvider(LocalSaveableStateRegistry provides registry) {
                    Box(Modifier.size(480.dp, 800.dp)) {
                        SessionDetailScreen(
                            state = SessionDetailUiState(
                                "session", SessionDto("session", "codex", status = "idle", updatedAt = "",
                                    title = "日本語タイトル", originalTitle = "Original title").toUiModel(),
                                messages, emptyList(), error = null, answering = false,
                            ),
                            resolveUrl = { it }, formatFileSize = { "$it bytes" }, formatTime = { "$it" }, onAction = {},
                        )
                    }
                }
            }
        }
        rule.onNodeWithText("日本語タイトル").assertExists()
        rule.onNodeWithText("日本語の返信").assertExists()
        rule.onNodeWithText("送信した英語を表示 · PLaMo").assertDoesNotExist()
        rule.onNodeWithText("原文を表示 · PLaMo").assertDoesNotExist()
        rule.onNodeWithContentDescription("Session actions").performClick()
        rule.onAllNodesWithText("原文を表示").assertCountEquals(1)
        rule.onNodeWithText("原文を表示").performClick()
        rule.onNodeWithText("Original title").assertExists()
        rule.onNodeWithText("Original reply").assertExists()
        rule.onNodeWithText("入力した日本語").assertDoesNotExist()
        // The prompt may also appear in the pinned outline, in the same language.
        rule.onAllNodesWithText("Sent English").fetchSemanticsNodes().also { check(it.isNotEmpty()) }
        var saved: Map<String, List<Any?>> = emptyMap()
        rule.runOnIdle { saved = registry.performSave(); visible = false }
        rule.waitForIdle()
        rule.runOnIdle { registry = SaveableStateRegistry(saved) { true }; visible = true }
        rule.onNodeWithText("Original reply").assertExists()
        rule.runOnIdle {
            messages = messages.dropLast(1) + messages.last().copy(blocks = listOf(
                BlockDto("text", text = "更新された返信", originalText = "Updated reply"),
            ))
        }
        rule.onNodeWithText("Updated reply").assertExists()
        rule.onNodeWithContentDescription("Session actions").performClick()
        rule.onNodeWithText("日本語表示に戻す").performClick()
        rule.onNodeWithText("日本語タイトル").assertExists()
        rule.onNodeWithText("更新された返信").assertExists()
    }

    @Test
    fun 翻訳履歴がなくても詳細にモードを表示し状態の変化を反映する() {
        var translation by mutableStateOf<Boolean?>(true)
        rule.setContent {
            MaterialTheme {
                SessionDetailScreen(
                    state = SessionDetailUiState(
                        "session", SessionDto("session", "codex", status = "offline", updatedAt = "",
                            title = "Session title", translation = translation).toUiModel(),
                        emptyList(), emptyList(), error = null, answering = false,
                    ),
                    resolveUrl = { it }, formatFileSize = { "$it bytes" }, formatTime = { "$it" }, onAction = {},
                )
            }
        }
        rule.onNodeWithText("Translation mode: On").assertDoesNotExist()
        rule.onNodeWithContentDescription("Translation mode: On").assertIsDisplayed()
        rule.runOnIdle { translation = false }
        rule.onNodeWithContentDescription("Translation mode: On").assertDoesNotExist()
        rule.runOnIdle { translation = null }
        rule.onNodeWithContentDescription("Translation mode: On").assertDoesNotExist()
        rule.runOnIdle { translation = true }
        rule.onNodeWithText("Session title").performClick()
        rule.onNodeWithText("Translation mode: On").assertExists()
        rule.runOnIdle { translation = false }
        rule.onNodeWithText("Translation mode: Off").assertExists()
        rule.runOnIdle { translation = null }
        rule.onNodeWithText("Translation mode: Unknown").assertExists()
    }

    @Test
    fun 翻訳のない会話には原文切替を出さない() {
        rule.setContent {
            MaterialTheme {
                SessionDetailScreen(
                    state = SessionDetailUiState(
                        "session", SessionDto("session", "codex", status = "idle", updatedAt = "").toUiModel(),
                        listOf(Message("a", "assistant", 0, listOf(BlockDto("text", text = "日本語のまま")))),
                        emptyList(), error = null, answering = false,
                    ),
                    resolveUrl = { it }, formatFileSize = { "$it bytes" }, formatTime = { "$it" }, onAction = {},
                )
            }
        }
        rule.onNodeWithContentDescription("Session actions").performClick()
        rule.onNodeWithText("原文を表示").assertDoesNotExist()
    }
}
