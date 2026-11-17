package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.MaterialTheme
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.platform.ClipEntry
import androidx.compose.ui.platform.Clipboard
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.junit4.v2.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performKeyInput
import androidx.compose.ui.test.performMouseInput
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.pressKey
import androidx.compose.ui.test.withKeyDown
import androidx.compose.ui.text.AnnotatedString
import com.tohutohu.herdrcompanion.data.api.SessionDto
import com.tohutohu.herdrcompanion.model.toUiModel
import com.tohutohu.herdrcompanion.ui.input.normalizePastedList
import com.tohutohu.herdrcompanion.ui.markdown.MarkdownText
import java.awt.datatransfer.DataFlavor
import java.awt.datatransfer.StringSelection
import java.awt.datatransfer.Transferable
import kotlin.test.assertEquals
import org.junit.Rule
import org.junit.Test

@OptIn(ExperimentalTestApi::class, ExperimentalComposeUiApi::class)
class ComposerListTest {
    @get:Rule val rule = createComposeRule()

    private val field get() = rule.onNode(hasSetTextAction())

    /** Paste and copy are Cmd on the Mac and Ctrl elsewhere (the Linux CI). */
    private val shortcutKey = if (System.getProperty("os.name").startsWith("Mac")) Key.MetaLeft else Key.CtrlLeft

    /**
     * Pastes through the field's paste action: a Ctrl+V sent right after the
     * click sometimes reached the field before its focus on the Linux CI, and
     * nothing was pasted.
     */
    private fun paste() {
        field.performSemanticsAction(SemanticsActions.PasteText)
        rule.waitForIdle()
    }

    /** The text that is sent; what is shown has "•" for the marks. */
    private fun assertText(text: String) {
        field.assert(SemanticsMatcher.expectValue(SemanticsProperties.InputText, AnnotatedString(text)))
    }

    private class FakeClipboard(var entry: ClipEntry?) : Clipboard {
        override suspend fun getClipEntry(): ClipEntry? = entry
        override suspend fun setClipEntry(clipEntry: ClipEntry?) { entry = clipEntry }
        override val nativeClipboard get() = error("unused")
    }

    private class HtmlSelection(private val text: String, private val html: String) : Transferable {
        private val htmlFlavor = DataFlavor("text/html;class=java.lang.String")
        override fun getTransferDataFlavors() = arrayOf(DataFlavor.stringFlavor, htmlFlavor)
        override fun isDataFlavorSupported(flavor: DataFlavor) = flavor in transferDataFlavors
        override fun getTransferData(flavor: DataFlavor): Any = if (flavor == htmlFlavor) html else text
    }

    private fun composer(clipboard: Clipboard = FakeClipboard(null), sent: MutableList<SessionDetailAction> = mutableListOf()) {
        val id = "claude:a"
        val state = SessionDetailUiState(
            id,
            SessionDto(id = id, provider = "claude", status = "idle", canSend = true, updatedAt = "").toUiModel(),
            emptyList(), emptyList(), error = null, answering = false,
            sendOnEnter = true,
        )
        rule.setContent {
            CompositionLocalProvider(LocalClipboard provides clipboard) {
                MaterialTheme {
                    SessionDetailScreen(
                        state = state,
                        resolveUrl = { it }, formatFileSize = { "$it bytes" }, formatTime = { "$it" },
                        drafts = ComposerDrafts(),
                        onAction = { sent += it },
                    )
                }
            }
        }
    }

    @Test
    fun 箇条書きの中のEnterは送信せず次の項目を始める() {
        val sent = mutableListOf<SessionDetailAction>()
        composer(sent = sent)
        field.performTextInput("- 一")
        field.performKeyInput { pressKey(Key.Enter) }
        assertText("- 一\n- ")
        field.performKeyInput { pressKey(Key.Tab) }
        field.performTextInput("子")
        field.performKeyInput { pressKey(Key.Enter) }
        assertText("- 一\n  - 子\n  - ")
        // An empty item steps out, then ends the list.
        field.performKeyInput { pressKey(Key.Enter) }
        assertText("- 一\n  - 子\n- ")
        field.performKeyInput { withKeyDown(Key.ShiftLeft) { pressKey(Key.Tab) } }
        assertText("- 一\n  - 子\n- ")
        field.performKeyInput { pressKey(Key.Enter) }
        assertText("- 一\n  - 子\n")
        rule.runOnIdle { assertEquals(emptyList(), sent) }
        field.performTextInput("以上")
        field.performKeyInput { pressKey(Key.Enter) }
        rule.runOnIdle { assertEquals(listOf<SessionDetailAction>(SessionDetailAction.Send("- 一\n  - 子\n以上")), sent) }
    }

    @Test
    fun ブラウザからコピーしたリストは入れ子を保って貼られる() {
        val html = "<meta charset='utf-8'><ul><li>一<ul><li>子</li></ul></li><li>二</li></ul>"
        composer(FakeClipboard(ClipEntry(HtmlSelection("一\n子\n二", html))))
        paste()
        assertText("- 一\n  - 子\n- 二")
    }

    @Test
    fun 中黒の箇条書きを貼るとMarkdownの項目になる() {
        composer(FakeClipboard(ClipEntry(StringSelection("・一\n・二"))))
        paste()
        assertText("- 一\n- 二")
    }

    @Test
    fun 会話の箇条書きを選択してコピーし貼ると入れ子のまま入る() {
        val clipboard = FakeClipboard(null)
        rule.setContent {
            CompositionLocalProvider(LocalClipboard provides clipboard) {
                MaterialTheme {
                    SelectionContainer(Modifier.testTag("md")) { MarkdownText("- 一\n  - 子\n- 二") }
                }
            }
        }
        rule.onNodeWithTag("md").performMouseInput {
            moveTo(Offset(1f, 1f))
            press()
            moveTo(Offset(width - 1f, height - 1f))
            release()
        }
        rule.onNodeWithTag("md").performKeyInput { withKeyDown(shortcutKey) { pressKey(Key.C) } }
        rule.waitForIdle()
        val copied = (clipboard.entry?.nativeClipEntry as Transferable).getTransferData(DataFlavor.stringFlavor) as String
        assertEquals("- 一\n  - 子\n- 二", normalizePastedList(copied))
    }
}
