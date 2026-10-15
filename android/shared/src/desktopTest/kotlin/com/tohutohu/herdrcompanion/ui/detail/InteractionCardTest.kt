package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.width
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.LocalSaveableStateRegistry
import androidx.compose.runtime.saveable.SaveableStateRegistry
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusManager
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.graphics.toPixelMap
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertIsToggleable
import androidx.compose.ui.test.assertIsOn
import androidx.compose.ui.test.assertIsOff
import androidx.compose.ui.test.assertIsSelected
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.api.InteractionDto
import com.tohutohu.herdrcompanion.data.api.InteractionResponseDto
import com.tohutohu.herdrcompanion.data.api.OptionDto
import com.tohutohu.herdrcompanion.data.api.QuestionDto
import java.awt.image.BufferedImage
import java.io.File
import javax.imageio.ImageIO
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue
import org.junit.Rule
import org.junit.Test

class InteractionCardTest {
    @get:Rule val rule = createComposeRule()

    private var visible by mutableStateOf(true)
    private var interactionId by mutableStateOf("question-1")
    private var questionType by mutableStateOf("multiselect")
    private var registry by mutableStateOf(SaveableStateRegistry(null) { true })
    private var response: InteractionResponseDto? = null
    private lateinit var focusManager: FocusManager

    private fun content() {
        rule.setContent {
            MaterialTheme {
                focusManager = LocalFocusManager.current
                if (visible) {
                    CompositionLocalProvider(LocalSaveableStateRegistry provides registry) {
                        Box(Modifier.width(500.dp)) {
                            InteractionCard(
                                interaction = InteractionDto(
                                    id = interactionId, type = "questions", state = "pending", supported = true,
                                    questions = listOf(
                                        QuestionDto("choices", questionType, question = "Choose features",
                                            options = listOf(OptionDto("Logs"), OptionDto("Images"))),
                                        QuestionDto("notes", "text", question = "Add instructions"),
                                    ),
                                ),
                                enabled = true,
                                onRespond = { response = it },
                                onOpenTerminal = {},
                            )
                        }
                    }
                }
            }
        }
    }

    private fun screenshot(name: String): IntArray {
        rule.runOnIdle { focusManager.clearFocus() }
        rule.mainClock.advanceTimeBy(1_000)
        val image: ImageBitmap = rule.onRoot().captureToImage()
        val pixels = image.toPixelMap()
        val argb = IntArray(image.width * image.height) { i -> pixels[i % image.width, i / image.width].toArgb() }
        val output = BufferedImage(image.width, image.height, BufferedImage.TYPE_INT_ARGB)
        output.setRGB(0, 0, image.width, image.height, argb, 0, image.width)
        val file = File("build/reports/audit-ui/$name.png")
        file.parentFile.mkdirs()
        ImageIO.write(output, "png", file)
        return argb
    }

    private fun enterAnswers() {
        rule.onNodeWithText("Logs").performClick()
        rule.onNodeWithText("Images").performClick()
        rule.onNode(hasSetTextAction()).performTextInput("日本語の追加指示")
        rule.onNodeWithText("Answer").assertIsEnabled()
    }

    @Test
    fun 保存した画面の再生成後も選択と自由入力を送信できる() {
        content()
        enterAnswers()
        val before = screenshot("question-before-recreation")
        var saved: Map<String, List<Any?>> = emptyMap()
        rule.runOnIdle {
            saved = registry.performSave()
            visible = false
        }
        rule.waitForIdle()
        rule.runOnIdle {
            registry = SaveableStateRegistry(saved) { true }
            visible = true
        }
        val after = screenshot("question-after-recreation")
        assertTrue(before.contentEquals(after), "画面再生成前後で入力表示が変わらない")
        rule.onNodeWithText("Answer").assertIsEnabled().performClick()

        rule.runOnIdle {
            assertEquals(listOf("Logs", "Images"), response?.answers?.get("choices")?.selected)
            assertEquals("日本語の追加指示", response?.answers?.get("notes")?.text)
        }
    }

    @Test
    fun 同じ位置に別の質問が来ても前の回答を送信しない() {
        content()
        enterAnswers()
        rule.runOnIdle { interactionId = "question-2" }
        rule.onNodeWithText("Answer").assertIsNotEnabled().performClick()
        rule.runOnIdle { assertNull(response) }
    }

    @Test
    fun 複数選択の行が一つのチェック可能な操作として状態を伝える() {
        content()
        rule.onNodeWithText("Logs").assertIsToggleable().assertIsOff().performClick().assertIsOn()
        rule.onAllNodes(
            SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.Checkbox), useUnmergedTree = true,
        ).assertCountEquals(2)
        rule.onNodeWithText("Logs").performClick().assertIsOff()
    }

    @Test
    fun 単一選択の行が選択状態を伝えて別の選択を解除する() {
        questionType = "single"
        content()
        rule.onNodeWithText("Logs").performClick().assertIsSelected()
        rule.onNodeWithText("Images").performClick().assertIsSelected()
        rule.onAllNodes(
            SemanticsMatcher.expectValue(SemanticsProperties.Role, Role.RadioButton), useUnmergedTree = true,
        ).assertCountEquals(2)
        rule.onNode(hasSetTextAction()).performTextInput("instructions")
        rule.onNodeWithText("Answer").performClick()
        rule.runOnIdle { assertEquals(listOf("Images"), response?.answers?.get("choices")?.selected) }
    }
}
