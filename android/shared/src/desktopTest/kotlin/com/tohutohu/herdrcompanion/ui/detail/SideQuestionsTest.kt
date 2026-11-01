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
    fun レーンごとの質問には自分のレーンの問答だけを履歴として渡す() {
        questions.ask("claude:a", "進捗は？")
        asked[0].answer.complete(SideAnswerDto("半分です"))
        questions.newLane("claude:a")
        assertTrue(questions["claude:a"].entries.isEmpty())
        questions.ask("claude:a", "allow の範囲は安全？")
        assertEquals(emptyList(), asked[1].history)
        asked[1].answer.complete(SideAnswerDto("読み取りだけです"))
        questions.ask("claude:a", "削除は？")
        assertEquals(listOf(SideTurnDto("allow の範囲は安全？", "読み取りだけです")), asked[2].history)

        val lanes = questions["claude:a"].lanes
        assertEquals(listOf("進捗は？", "allow の範囲は安全？"), lanes.map { it.title })
        assertTrue(lanes[1].pending)
        // The first lane is asked while the second still waits.
        questions.selectLane("claude:a", lanes[0].id)
        questions.ask("claude:a", "残りは？")
        assertEquals(listOf(SideTurnDto("進捗は？", "半分です")), asked[3].history)
        assertEquals(listOf("進捗は？", "残りは？"), questions["claude:a"].entries.map { it.question })
    }

    @Test
    fun 消すのは表示中のレーンだけで残ったうち最新のレーンを表示する() {
        questions.ask("claude:a", "一つ目")
        questions.newLane("claude:a")
        questions.ask("claude:a", "二つ目")
        questions.newLane("claude:a")
        questions.ask("claude:a", "三つ目")
        questions.selectLane("claude:a", questions["claude:a"].lanes[1].id)
        questions.clear("claude:a")
        assertEquals(listOf("一つ目", "三つ目"), questions["claude:a"].lanes.map { it.title })
        assertEquals("三つ目", questions["claude:a"].active?.title)
    }

    @Test
    fun 消した後に届いた答えは戻ってこない() {
        questions.ask("claude:a", "長くかかる質問")
        questions.clear("claude:a")
        asked[0].answer.complete(SideAnswerDto("遅れた答え"))
        assertTrue(questions["claude:a"].entries.isEmpty())
    }

    @Test
    fun ペインでは添付メニューからbtwを開くと入力欄の代わりに脇道の会話が出て質問できる() {
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
        // Until something is asked, btw is only in the attach menu.
        rule.onNodeWithText("btw").assertDoesNotExist()
        rule.onNodeWithContentDescription("Attach").performClick()
        rule.onNodeWithText("Ask on the side (btw)").performClick()
        rule.onNodeWithText("Not added to the conversation").assertExists()
        rule.onNodeWithContentDescription("Send").assertDoesNotExist()
        rule.onNode(hasSetTextAction()).performTextInput("receipts.go を読んだのはなぜ？")
        rule.onNodeWithContentDescription("Ask").performClick()
        rule.runOnIdle { asked.single().answer.complete(SideAnswerDto("受信確認を見るためです")) }
        rule.onNodeWithText("受信確認を見るためです").assertExists()

        rule.onNodeWithContentDescription("Close btw").performClick()
        rule.onNodeWithContentDescription("Send").assertExists()
        // Closed, a button beside send counts what is kept and opens it again.
        rule.onNodeWithText("1").assertExists()
        rule.onNodeWithText("btw").performClick()
        rule.onNodeWithText("受信確認を見るためです").assertExists()
    }

    @Test
    fun レーンのチップで切り替えるとそのレーンの問答だけが出る() {
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
        rule.onNodeWithContentDescription("Attach").performClick()
        rule.onNodeWithText("Ask on the side (btw)").performClick()
        rule.onNode(hasSetTextAction()).performTextInput("一つ目のレーン")
        rule.onNodeWithContentDescription("Ask").performClick()
        rule.runOnIdle { asked[0].answer.complete(SideAnswerDto("一つ目の答え")) }

        rule.onNodeWithText("+ New").performClick()
        rule.onNode(hasSetTextAction()).performTextInput("二つ目のレーン")
        rule.onNodeWithContentDescription("Ask").performClick()
        rule.runOnIdle { asked[1].answer.complete(SideAnswerDto("二つ目の答え")) }
        rule.onNodeWithText("二つ目の答え").assertExists()
        rule.onNodeWithText("一つ目の答え").assertDoesNotExist()

        rule.onNodeWithText("一つ目のレーン").performClick()
        rule.onNodeWithText("一つ目の答え").assertExists()
        rule.onNodeWithText("二つ目の答え").assertDoesNotExist()
        rule.runOnIdle { assertTrue(asked[1].history.isEmpty()) }
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
        rule.onNodeWithContentDescription("Attach").performClick()
        rule.onNodeWithText("File").assertExists()
        rule.onNodeWithText("Ask on the side (btw)").assertDoesNotExist()
    }
}
