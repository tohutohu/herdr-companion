package com.tohutohu.herdrcompanion

import androidx.compose.foundation.layout.Column
import androidx.compose.material3.Button
import androidx.compose.material3.Text
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.swipe
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.tohutohu.herdrcompanion.data.Message
import com.tohutohu.herdrcompanion.data.api.BlockDto
import com.tohutohu.herdrcompanion.data.api.InteractionDto
import com.tohutohu.herdrcompanion.data.api.InteractionResponseDto
import com.tohutohu.herdrcompanion.data.api.OptionDto
import com.tohutohu.herdrcompanion.data.api.QuestionDto
import com.tohutohu.herdrcompanion.test.DeviceTestActivity
import com.tohutohu.herdrcompanion.ui.detail.AttachmentUiState
import com.tohutohu.herdrcompanion.ui.detail.InteractionCard
import com.tohutohu.herdrcompanion.ui.detail.SessionDetailScreen
import com.tohutohu.herdrcompanion.ui.detail.SessionDetailUiState
import com.tohutohu.herdrcompanion.ui.detail.rememberPickedAttachments
import java.io.File
import java.util.concurrent.atomic.AtomicReference
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/** Real Android saved-state tests; these do not claim to simulate OS process death. */
@RunWith(AndroidJUnit4::class)
class DeviceStateRestorationTest {
    @get:Rule val rule = createEmptyComposeRule()
    private var scenario: ActivityScenario<DeviceTestActivity>? = null

    @After fun closeActivity() {
        scenario?.close()
        DeviceTestActivity.content = {}
    }

    @Test fun 質問回答をActivity再生成後も日本語と選択を変えず送信できる() {
        val response = AtomicReference<InteractionResponseDto>()
        DeviceTestActivity.content = {
            InteractionCard(InteractionDto(id = "question", type = "questions", state = "pending", supported = true,
                questions = listOf(
                    QuestionDto("features", "multiselect", question = "Choose features", options = listOf(OptionDto("Logs"), OptionDto("Images"))),
                    QuestionDto("notes", "text", question = "Add instructions"),
                )), enabled = true, onRespond = response::set, onOpenTerminal = {})
        }
        launch()
        rule.onNodeWithText("Logs").performClick()
        rule.onNodeWithText("Images").performClick()
        rule.onNode(hasSetTextAction()).performTextInput("日本語の追加指示🙂")
        scenario!!.recreate()
        rule.onNodeWithText("日本語の追加指示🙂").assertExists()
        rule.onNodeWithText("Answer").assertIsEnabled().performClick()
        assertEquals(listOf("Logs", "Images"), response.get().answers?.get("features")?.selected)
        assertEquals("日本語の追加指示🙂", response.get().answers?.get("notes")?.text)
        screenshot("question-activity-restored")
    }

    @Test fun 選択添付の識別子とメタデータをAndroidBundleから復元できる() {
        val observed = AtomicReference<List<AttachmentUiState>>(emptyList())
        val picked = AttachmentUiState("content://device-test/document/42", "report-日本語.txt", "text/plain", 123_456L)
        DeviceTestActivity.content = {
            val attachments = rememberPickedAttachments("session")
            SideEffect { observed.set(attachments.toList()) }
            Column {
                Button(onClick = { attachments.add(picked) }) { Text("Add attachment") }
                attachments.forEach { Text(it.name) }
            }
        }
        launch()
        rule.onNodeWithText("Add attachment").performClick()
        rule.onNodeWithText(picked.name).assertExists()
        scenario!!.recreate()
        rule.onNodeWithText(picked.name).assertExists()
        assertEquals(listOf(picked), observed.get())
    }

    @Test fun 履歴閲覧位置をActivity再生成後も保ち追加出力で最新へ飛ばない() {
        fun message(index: Int) = Message("message-$index", if (index % 2 == 0) "assistant" else "user", index.toLong(),
            listOf(BlockDto("text", text = "Message $index paragraph one\n\nMessage $index paragraph two")))
        var messages by mutableStateOf((1..50).map(::message))
        DeviceTestActivity.content = {
            SessionDetailScreen(SessionDetailUiState("session", null, messages, emptyList(), error = null, answering = false),
                resolveUrl = { it }, formatFileSize = { "$it bytes" }, onAction = {})
        }
        launch()
        val transcript = SemanticsMatcher.keyIsDefined(SemanticsActions.ScrollToIndex)
        repeat(4) {
            rule.onNode(transcript).performTouchInput {
                swipe(Offset(centerX, height * 0.72f), Offset(centerX, height * 0.96f))
            }
        }
        fun scroll() = rule.onNode(transcript).fetchSemanticsNode().config[SemanticsProperties.VerticalScrollAxisRange].value()
        rule.waitForIdle()
        assertTrue("履歴へ移動した", scroll() > 0f)
        scenario!!.recreate()
        rule.waitForIdle()
        assertTrue("Bundleから履歴位置を復元した", scroll() > 0f)
        rule.runOnIdle { messages = messages + message(51) }
        rule.waitForIdle()
        assertTrue("追加出力後も履歴閲覧を継続した", scroll() > 0f)
        screenshot("transcript-activity-restored")
    }

    private fun launch() {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        check(context.packageName.endsWith(".devicetest")) { "Device tests require the isolated APK" }
        scenario = ActivityScenario.launch(DeviceTestActivity::class.java)
        rule.waitForIdle()
    }

    private fun screenshot(name: String) {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val output = File(context.getExternalFilesDir(null), "device-test-screenshots/$name.png")
        output.parentFile!!.mkdirs()
        output.outputStream().use { rule.onRoot().captureToImage().asAndroidBitmap().compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }
    }
}
