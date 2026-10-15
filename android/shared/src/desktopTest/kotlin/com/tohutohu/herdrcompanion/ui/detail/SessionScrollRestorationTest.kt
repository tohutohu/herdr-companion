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
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.swipe
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.Message
import com.tohutohu.herdrcompanion.data.api.BlockDto
import kotlin.test.assertTrue
import org.junit.Rule
import org.junit.Test

class SessionScrollRestorationTest {
    @get:Rule val rule = createComposeRule()

    @Test
    fun 履歴を読んだまま画面を再生成しても次の出力で最新へ飛ばない() {
        var visible by mutableStateOf(true)
        var registry by mutableStateOf(SaveableStateRegistry(null) { true })
        fun message(index: Int) = Message("message-$index", if (index % 2 == 0) "assistant" else "user", index.toLong(), listOf(
            BlockDto("text", text = (1..2).joinToString("\n\n") { "Message $index paragraph $it" }),
        ))
        var messages by mutableStateOf((1..30).map(::message))
        rule.setContent {
            MaterialTheme {
                if (visible) CompositionLocalProvider(LocalSaveableStateRegistry provides registry) {
                    Box(Modifier.size(500.dp)) {
                        SessionDetailScreen(
                            state = SessionDetailUiState("session", null, messages, emptyList(), error = null, answering = false),
                            resolveUrl = { it }, formatFileSize = { "$it bytes" }, onAction = {},
                        )
                    }
                }
            }
        }
        rule.mainClock.advanceTimeBy(1_000)
        val transcript = SemanticsMatcher.keyIsDefined(SemanticsActions.ScrollToIndex)
        repeat(3) {
            rule.onNode(transcript).performTouchInput {
                // Stay below the pinned turn outline so the gesture reaches
                // the transcript rather than the outline's own scroll area.
                swipe(Offset(centerX, height * 0.72f), Offset(centerX, height * 0.97f))
            }
        }
        rule.waitForIdle()
        fun scroll() = rule.onNode(transcript).fetchSemanticsNode()
            .config[SemanticsProperties.VerticalScrollAxisRange].value()
        assertTrue(scroll() > 0f, "履歴へスクロールしている")
        rule.runOnIdle { messages = messages + message(31) }
        rule.mainClock.advanceTimeBy(1_000)
        assertTrue(scroll() > 0f, "再生成前は新しい出力が届いても履歴閲覧を継続できる")
        var saved: Map<String, List<Any?>> = emptyMap()
        rule.runOnIdle { saved = registry.performSave(); visible = false }
        rule.waitForIdle()
        rule.runOnIdle { registry = SaveableStateRegistry(saved) { true }; visible = true }
        rule.waitForIdle()
        assertTrue(scroll() > 0f, "画面再生成で履歴位置が復元される")
        rule.runOnIdle { messages = messages + message(32) }
        rule.mainClock.advanceTimeBy(1_000)
        assertTrue(scroll() > 0f, "新しい出力が届いても履歴閲覧を継続できる")
    }
}
