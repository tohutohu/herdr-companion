package com.tohutohu.herdrcompanion.desktop

import androidx.compose.foundation.layout.size
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.toAwtImage
import androidx.compose.ui.test.SemanticsNodeInteractionsProvider
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.Settings
import com.tohutohu.herdrcompanion.ui.theme.SharedTheme
import java.io.File
import javax.imageio.ImageIO
import okhttp3.OkHttpClient
import org.junit.Assume.assumeTrue
import org.junit.Rule
import org.junit.Test

/**
 * Drives the Mac UI's 常駐 view against a throwaway gateway with real agents:
 * create → run → report → follow-up → stop. Runs only when
 * HERDR_RESIDENT_E2E_URL / _TOKEN / _CWD are set (docs/resident.md); the
 * normal test run never contacts a gateway.
 */
class DesktopResidentFlowTest {
    @get:Rule
    val rule = createComposeRule()

    private val url = System.getenv("HERDR_RESIDENT_E2E_URL").orEmpty()
    private val token = System.getenv("HERDR_RESIDENT_E2E_TOKEN").orEmpty()
    private val cwd = System.getenv("HERDR_RESIDENT_E2E_CWD").orEmpty()
    private val shots = File(System.getenv("HERDR_RESIDENT_E2E_SHOTS") ?: "build/reports/resident-e2e").also { it.mkdirs() }

    private fun SemanticsNodeInteractionsProvider.waitText(text: String, timeoutMs: Long = 60_000) =
        rule.waitUntil(timeoutMs) { onAllNodesWithText(text, substring = true, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() }

    private fun shot(name: String) {
        rule.waitForIdle()
        ImageIO.write(rule.onRoot().captureToImage().toAwtImage(), "png", File(shots, "$name.png"))
    }

    /** Waits until the conversation shows [n] reports (the task list also mentions results). */
    private fun waitReports(n: Int) = rule.waitUntil(240_000) {
        rule.onAllNodesWithText("報告 · ", substring = true, useUnmergedTree = true).fetchSemanticsNodes().size >= n
    }

    /** Clicks the copy of [text] that is on screen (the form has two save buttons). */
    private fun clickVisible(text: String) {
        val nodes = rule.onAllNodesWithText(text)
        val i = nodes.fetchSemanticsNodes().indexOfFirst { it.boundsInRoot.width > 0f }
        nodes[i].performClick()
    }

    @Test
    fun 常駐ビューで作成し実行して報告を受け追加指示を送り停止できる() {
        assumeTrue("set HERDR_RESIDENT_E2E_URL to run", url.isNotEmpty())
        val state = DesktopResidentState({ Settings(url, token) }, OkHttpClient())
        val name = "desktop e2e ${System.currentTimeMillis() % 100000}"
        rule.setContent { SharedTheme { DesktopResidentView(state, Modifier.size(1280.dp, 900.dp)) } }
        rule.waitUntil(60_000) { rule.onAllNodes(androidx.compose.ui.test.hasContentDescription("新しいタスク")).fetchSemanticsNodes().isNotEmpty() }
        shot("1-home")
        rule.onNode(androidx.compose.ui.test.hasContentDescription("新しいタスク")).performClick()
        rule.waitText("新しい常駐タスク")
        rule.onNode(hasSetTextAction() and hasText("タスク名")).performTextInput(name)
        rule.onNode(hasSetTextAction() and hasText("指示")).performTextInput("Read README.md and report the fruit it names. Keep it very short.")
        rule.onNode(hasSetTextAction() and hasText("作業フォルダ（workspaceRoots 内）")).performTextInput(cwd)
        rule.onNodeWithText("Codex").performClick()
        rule.waitText("モデル: 既定")
        rule.onNodeWithText("モデル: 既定").performScrollTo().performClick()
        rule.waitText("GPT-6-Luna")
        rule.onNodeWithText("GPT-6-Luna").performClick()
        rule.onNodeWithText("effort: 既定").performClick()
        rule.waitText("Low")
        rule.onNodeWithText("Low").performClick()
        rule.onNodeWithText("手動のみ").performScrollTo().performClick()
        shot("2-form")
        clickVisible("保存")
        rule.waitText("このフォルダを信頼する")
        rule.onNodeWithText("このフォルダを信頼する", substring = true).performScrollTo().performClick()
        shot("3-trust")
        clickVisible("保存")
        rule.waitText("今すぐ実行")
        rule.onNodeWithText("今すぐ実行").performClick()
        rule.waitText("今回の実行を停止", 30_000)
        shot("4-running")
        waitReports(1)
        shot("5-report")
        rule.onNode(hasSetTextAction() and hasText("追加指示（例: 詳しく調べて）")).performTextInput("Which fruit did the last run find? Answer from the memory only.")
        rule.onNode(androidx.compose.ui.test.hasContentDescription("追加指示を送る")).performClick()
        rule.waitText("Which fruit did the last run find?")
        waitReports(2)
        shot("6-follow-up")
        rule.onNodeWithText("今すぐ実行").performClick()
        rule.waitText("今回の実行を停止", 30_000)
        rule.onNodeWithText("今回の実行を停止").performClick()
        rule.onNodeWithText("実行履歴").performClick()
        rule.waitText("停止", 60_000)
        rule.waitText("ユーザーが停止しました", 60_000)
        shot("7-stopped")
        state.close()
    }
}
