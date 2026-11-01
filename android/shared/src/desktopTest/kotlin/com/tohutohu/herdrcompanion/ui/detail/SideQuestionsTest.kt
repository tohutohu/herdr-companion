package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import com.tohutohu.herdrcompanion.data.api.SessionDto
import com.tohutohu.herdrcompanion.data.api.SideAnswerDto
import com.tohutohu.herdrcompanion.data.api.SideTurnDto
import com.tohutohu.herdrcompanion.model.toUiModel
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlin.test.assertEquals
import kotlin.test.assertTrue
import org.junit.Rule
import org.junit.Test

class SideQuestionsTest {
    @get:Rule val rule = createComposeRule()

    private class Asked(val sessionId: String, val question: String, val history: List<SideTurnDto>) {
        val answer = CompletableDeferred<SideAnswerDto>()
    }

    private val asked = mutableListOf<Asked>()
    private val questions = SideQuestions(CoroutineScope(Dispatchers.Unconfined)) { id, question, history ->
        Asked(id, question, history).also { asked += it }.answer.await()
    }

    private fun state(id: String) = SessionDetailUiState(
        id,
        SessionDto(id = id, provider = id.substringBefore(':'), status = "running", canSend = true, updatedAt = "").toUiModel(),
        emptyList(), emptyList(), error = null, answering = false,
    )

    @Test
    fun 答えが来るまでは待ちで続けて聞くと前の問答を履歴として渡す() {
        questions.ask("claude:a", "  いま何してる？ ")
        assertTrue(questions["claude:a"].entries.single().pending)
        asked[0].answer.complete(SideAnswerDto("テストを読んでいます"))
        assertEquals("テストを読んでいます", questions["claude:a"].entries.single().answer)

        questions.ask("claude:a", "なぜ？")
        assertEquals("いま何してる？", asked[0].question)
        assertEquals(listOf(SideTurnDto("いま何してる？", "テストを読んでいます")), asked[1].history)
        assertTrue(questions["claude:b"].entries.isEmpty())
    }

    @Test
    fun 失敗した質問は同じ履歴のままやり直せる() {
        questions.ask("claude:a", "一つ目")
        asked[0].answer.complete(SideAnswerDto("答え"))
        questions.ask("claude:a", "二つ目")
        asked[1].answer.completeExceptionally(IllegalStateException("session is not running"))
        val failed = questions["claude:a"].entries[1]
        assertEquals("session is not running", failed.error)

        questions.retry("claude:a", failed.id)
        assertTrue(questions["claude:a"].entries[1].pending)
        assertEquals(asked[1].history, asked[2].history)
        asked[2].answer.complete(SideAnswerDto("今度は答え"))
        assertEquals(listOf("答え", "今度は答え"), questions["claude:a"].entries.map { it.answer })
    }

    @Test
    fun 消した後に届いた答えは戻ってこない() {
        questions.ask("claude:a", "長くかかる質問")
        questions.clear("claude:a")
        asked[0].answer.complete(SideAnswerDto("遅れた答え"))
        assertTrue(questions["claude:a"].entries.isEmpty())
    }

    @Test
    fun ペインではbtwを開くと入力欄の代わりに脇道の会話が出て質問できる() {
        rule.setContent {
            MaterialTheme {
                SessionDetailScreen(
                    state = state("claude:a"),
                    resolveUrl = { it }, formatFileSize = { "$it bytes" }, formatTime = { "$it" },
                    sideQuestions = questions,
                    sideQuestionsInPane = true,
                    onAction = {},
                )
            }
        }
        rule.onNodeWithText("btw").performClick()
        rule.onNodeWithText("Not added to the conversation").assertExists()
        rule.onNodeWithContentDescription("Send").assertDoesNotExist()
        rule.onNode(hasSetTextAction()).performTextInput("receipts.go を読んだのはなぜ？")
        rule.onNodeWithContentDescription("Ask").performClick()
        rule.runOnIdle { asked.single().answer.complete(SideAnswerDto("受信確認を見るためです")) }
        rule.onNodeWithText("受信確認を見るためです").assertExists()

        rule.onNodeWithContentDescription("Close btw").performClick()
        rule.onNodeWithContentDescription("Send").assertExists()
        // Closed, the button counts what is kept.
        rule.onNodeWithText("1").assertExists()
    }

    @Test
    fun Codexのセッションにはbtwを出さない() {
        rule.setContent {
            MaterialTheme {
                SessionDetailScreen(
                    state = state("codex:a"),
                    resolveUrl = { it }, formatFileSize = { "$it bytes" }, formatTime = { "$it" },
                    sideQuestions = questions,
                    sideQuestionsInPane = true,
                    onAction = {},
                )
            }
        }
        rule.onNodeWithContentDescription("Send").assertExists()
        rule.onNodeWithText("btw").assertDoesNotExist()
    }
}
