package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import com.tohutohu.herdrcompanion.data.api.SessionDto
import com.tohutohu.herdrcompanion.model.toUiModel
import kotlin.test.assertEquals
import org.junit.Rule
import org.junit.Test

class ComposerDraftRetentionTest {
    @get:Rule val rule = createComposeRule()

    private fun state(id: String, status: String = "idle") = SessionDetailUiState(
        id,
        SessionDto(id = id, provider = id.substringBefore(':'), status = status, canSend = status != "offline", updatedAt = "").toUiModel(),
        emptyList(), emptyList(), error = null, answering = false,
    )

    private val field get() = rule.onNode(hasSetTextAction())

    /** The field's own text; its placeholder is reported as text too. */
    private fun assertDraft(text: String) {
        field.assert(SemanticsMatcher.expectValue(SemanticsProperties.EditableText, AnnotatedString(text)))
    }

    @Test
    fun 別のセッションへ移って戻ると入力途中の文字が残っている() {
        val drafts = ComposerDrafts()
        var sessionId by mutableStateOf("claude:a")
        rule.setContent {
            MaterialTheme {
                SessionDetailScreen(
                    state = state(sessionId),
                    resolveUrl = { it }, formatFileSize = { "$it bytes" }, formatTime = { "$it" },
                    drafts = drafts,
                    onAction = {},
                )
            }
        }
        field.performTextInput("途中まで書いた指示")
        rule.runOnIdle { sessionId = "codex:b" }
        assertDraft("")
        field.performTextInput("別セッションの下書き")
        rule.runOnIdle { sessionId = "claude:a" }
        assertDraft("途中まで書いた指示")
        rule.runOnIdle { sessionId = "codex:b" }
        assertDraft("別セッションの下書き")
    }

    @Test
    fun 再開バーに切り替わって戻っても入力途中の文字が残っている() {
        val drafts = ComposerDrafts()
        var status by mutableStateOf("idle")
        rule.setContent {
            MaterialTheme {
                SessionDetailScreen(
                    state = state("claude:a", status),
                    resolveUrl = { it }, formatFileSize = { "$it bytes" }, formatTime = { "$it" },
                    drafts = drafts,
                    onAction = {},
                )
            }
        }
        field.performTextInput("止まる前の下書き")
        rule.runOnIdle { status = "offline" }
        rule.waitUntil { rule.onAllNodes(hasSetTextAction()).fetchSemanticsNodes().isEmpty() }
        rule.runOnIdle { status = "idle" }
        assertDraft("止まる前の下書き")
    }

    @Test
    fun 送信すると下書きは消える() {
        val drafts = ComposerDrafts().apply { set("claude:a", "送る内容") }
        var sent: SessionDetailAction? = null
        rule.setContent {
            MaterialTheme {
                SessionDetailScreen(
                    state = state("claude:a"),
                    resolveUrl = { it }, formatFileSize = { "$it bytes" }, formatTime = { "$it" },
                    drafts = drafts,
                    onAction = { sent = it },
                )
            }
        }
        assertDraft("送る内容")
        rule.onNodeWithContentDescription("Send").performClick()
        rule.runOnIdle {
            assertEquals(SessionDetailAction.Send("送る内容"), sent)
            assertEquals("", drafts["claude:a"])
        }
        assertDraft("")
    }
}
