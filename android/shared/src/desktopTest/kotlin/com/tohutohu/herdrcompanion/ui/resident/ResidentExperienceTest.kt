package com.tohutohu.herdrcompanion.ui.resident

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.toAwtImage
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.isDisplayed
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performScrollToIndex
import androidx.compose.ui.test.performTextReplacement
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.api.LimitsDto
import com.tohutohu.herdrcompanion.data.api.ModelsResponse
import com.tohutohu.herdrcompanion.data.api.ResidentMessageDto
import com.tohutohu.herdrcompanion.data.api.ResidentMessagesResponse
import com.tohutohu.herdrcompanion.data.api.ResidentRunsResponse
import com.tohutohu.herdrcompanion.data.api.ResidentTaskDetailDto
import com.tohutohu.herdrcompanion.data.api.ResidentTaskDto
import com.tohutohu.herdrcompanion.data.api.ResidentTaskRequest
import java.lang.reflect.Proxy
import java.io.File
import javax.imageio.ImageIO
import kotlin.test.assertEquals
import kotlin.test.assertTrue
import org.junit.Rule
import org.junit.Test

/** No network or agent calls: exercise the same Compose screens on a phone-sized surface. */
class ResidentExperienceTest {
    @get:Rule val rule = createComposeRule()

    private fun backend(call: (String, Array<out Any?>) -> Any? = { name, _ -> error("Unexpected $name") }): ResidentBackend =
        Proxy.newProxyInstance(ResidentBackend::class.java.classLoader, arrayOf(ResidentBackend::class.java)) { proxy, method, args ->
            when (method.name) {
                "equals" -> proxy === args?.firstOrNull()
                "hashCode" -> System.identityHashCode(proxy)
                "toString" -> "ResidentTestBackend"
                "getConnectionKey" -> "resident-test"
                "formatInstant" -> args!![0]
                "newRequestId" -> "request-test"
                "models" -> ModelsResponse()
                else -> call(method.name, args ?: emptyArray())
            }
        } as ResidentBackend

    private fun report(i: Int) = ResidentMessageDto("report-$i", role = "assistant", kind = "report", text = "報告本文 $i", createdAt = "時刻 $i")

    private fun screenshot(name: String) {
        val dir = System.getenv("HERDR_RESIDENT_UI_SHOTS")?.let(::File) ?: return
        dir.mkdirs()
        ImageIO.write(rule.onRoot().captureToImage().toAwtImage(), "png", File(dir, "$name.png"))
    }

    @Test
    fun 詳細を開くと実行情報をたたんで報告を読める() {
        val task = ResidentTaskDto("task", "毎朝のCIチェック", "CIを確認", "claude", cwd = "/workspace",
            nextRunAt = "明日 07:00", runsToday = 2)
        val backend = backend { method, _ ->
            when (method) {
                "task" -> ResidentTaskDetailDto(task, upcoming = listOf("明日 07:00", "明後日 07:00"))
                "messages" -> ResidentMessagesResponse(listOf(
                    report(1).copy(text = "昨日の確認では対応が必要な問題はありませんでした。"),
                    report(2).copy(text = "CI が復旧しました。\n\nテストとビルドが通り、PR をレビューできる状態になっています。"),
                ))
                "runs" -> ResidentRunsResponse()
                else -> error("Unexpected $method")
            }
        }
        rule.setContent {
            MaterialTheme {
                Box(Modifier.size(420.dp, 800.dp)) {
                    ResidentTaskScreen(backend, task.id, {}, {}, {})
                }
            }
        }
        rule.onNodeWithText("CI が復旧しました。").assertIsDisplayed()
        rule.onNodeWithText("今後の予定:", substring = true).assertDoesNotExist()
        screenshot("reports-phone")
        rule.onNodeWithText("実行情報").performClick()
        rule.onNodeWithText("今後の予定:", substring = true).assertExists()
    }

    @Test
    fun 最新の報告を上から読み進められる() {
        val backend = backend()
        rule.setContent {
            MaterialTheme {
                Box(Modifier.size(420.dp, 700.dp)) {
                    ResidentReports((1..3).map(::report), backend, Modifier, false, true, null)
                }
            }
        }
        val positions = (3 downTo 1).map { rule.onNodeWithText("報告本文 $it").fetchSemanticsNode().boundsInRoot.top }
        assertTrue(positions.zipWithNext().all { (a, b) -> a < b })
        rule.onNodeWithText("最新の報告へ").assertIsNotEnabled()
    }

    @Test
    fun 読んでいる報告は更新で動かずボタンで最新へ戻れる() {
        var reports by mutableStateOf((1..30).map(::report))
        val backend = backend()
        rule.setContent {
            MaterialTheme {
                Box(Modifier.size(420.dp, 500.dp)) {
                    ResidentReports(reports, backend, Modifier, false, true, null)
                }
            }
        }
        rule.onNode(SemanticsMatcher.keyIsDefined(SemanticsActions.ScrollToIndex)).performScrollToIndex(12)
        val before = rule.onNodeWithText("報告本文 18").fetchSemanticsNode().boundsInRoot.top
        rule.runOnIdle { reports = reports + report(31) }
        rule.onNodeWithText("報告本文 18").assertIsDisplayed()
        assertEquals(before, rule.onNodeWithText("報告本文 18").fetchSemanticsNode().boundsInRoot.top)
        rule.onNodeWithText("最新の報告へ").performClick()
        rule.onNodeWithText("報告本文 31").assertIsDisplayed()
    }

    @Test
    fun 設定の上限を編集して保存し不正な値なら保存できない() {
        val task = ResidentTaskDto("task", "CI確認", "CIを確認", "claude", cwd = "/workspace", version = 7,
            limits = LimitsDto(45, 24, "2027-01-01T00:00:00Z"))
        var saved: ResidentTaskRequest? = null
        val backend = backend { method, args ->
            when (method) {
                "task" -> ResidentTaskDetailDto(task)
                "update" -> { saved = args[1] as ResidentTaskRequest; task }
                else -> error("Unexpected $method")
            }
        }
        rule.setContent {
            MaterialTheme {
                Box(Modifier.size(420.dp, 800.dp)) {
                    ResidentTaskForm(backend, task.id, "Asia/Tokyo", {}, {})
                }
            }
        }
        val count = rule.onNode(hasSetTextAction() and hasText("1日の起動上限（回）"))
        count.performScrollTo().performTextReplacement("0")
        rule.onNodeWithText("1〜288 回で入力してください").assertExists()
        rule.onAllNodesWithText("保存")[0].assertIsNotEnabled()
        count.performTextReplacement("48")
        rule.onNode(hasSetTextAction() and hasText("1回の制限時間（分）")).performScrollTo().performTextReplacement("60")
        screenshot("limits-phone")
        val saves = rule.onAllNodesWithText("保存")
        saves[saves.fetchSemanticsNodes().indices.first { saves[it].isDisplayed() }].performClick()
        rule.runOnIdle {
            assertEquals(LimitsDto(60, 48, task.limits.expiresAt), saved?.limits)
            assertEquals(7L, saved?.expectedVersion)
        }
    }
}
