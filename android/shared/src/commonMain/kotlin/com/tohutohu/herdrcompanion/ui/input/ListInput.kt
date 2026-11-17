package com.tohutohu.herdrcompanion.ui.input

import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.remember
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEvent
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isAltPressed
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isMetaPressed
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.ClipEntry
import androidx.compose.ui.platform.Clipboard
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.platform.NativeClipboard
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.input.OffsetMapping
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.text.input.TransformedText
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.em
import com.tohutohu.herdrcompanion.ui.detail.plainTextClipEntry

/**
 * A hardware key on a list item: Return starts the next item (Shift+Return a
 * new line inside it), Tab and Shift+Tab nest it. Null when the key is not
 * for the list, so the field (or sending) handles it.
 */
internal fun listKeyEdit(event: KeyEvent, value: TextFieldValue): TextFieldValue? {
    if (event.type != KeyEventType.KeyDown) return null
    if (event.isMetaPressed || event.isCtrlPressed || event.isAltPressed) return null
    return when (event.key) {
        Key.Enter, Key.NumPadEnter ->
            if (event.isShiftPressed) breakInsideListItem(value) else continueList(value)
        Key.Tab -> indentListItems(value, outdent = event.isShiftPressed)
        else -> null
    }
}

/** Text and HTML of a clipboard entry, as far as the platform exposes them. */
internal expect fun ClipEntry.readTextAndHtml(): Pair<String?, String?>

/**
 * Pastes in [content]'s text fields take a list from the clipboard's HTML
 * when its plain text has lost the bullets (as browsers write it).
 */
@Composable
internal fun ListPasteScope(content: @Composable () -> Unit) {
    val base = LocalClipboard.current
    val clipboard = remember(base) { ListPasteClipboard(base) }
    CompositionLocalProvider(LocalClipboard provides clipboard, content = content)
}

private class ListPasteClipboard(private val base: Clipboard) : Clipboard {
    override suspend fun getClipEntry(): ClipEntry? {
        val entry = base.getClipEntry() ?: return null
        val list = runCatching {
            val (text, html) = entry.readTextAndHtml()
            listTextForPaste(html, text)
        }.getOrNull() ?: return entry
        return plainTextClipEntry(list)
    }

    override suspend fun setClipEntry(clipEntry: ClipEntry?) = base.setClipEntry(clipEntry)

    override val nativeClipboard: NativeClipboard get() = base.nativeClipboard
}

private val INDENT = SpanStyle(letterSpacing = 0.45.em)

/** Shows "-", "*" and "+" items with a bullet, and their marks dimmed. Offsets are unchanged. */
internal class ListBulletTransformation(private val markColor: Color) : VisualTransformation {
    override fun filter(text: AnnotatedString): TransformedText {
        val source = text.text
        val chars = StringBuilder(source)
        val marks = mutableListOf<Pair<Int, Int>>()
        val indents = mutableListOf<Pair<Int, Int>>()
        var start = 0
        for (line in source.split('\n')) {
            parseListItem(line)?.let { item ->
                val at = start + item.indent.length
                if (!item.ordered) chars.setCharAt(at, '•')
                marks += at to start + item.length
                if (item.indent.isNotEmpty()) indents += start to at
            }
            start += line.length + 1
        }
        if (marks.isEmpty()) return TransformedText(text, OffsetMapping.Identity)
        val style = SpanStyle(color = markColor)
        val shown = buildAnnotatedString {
            append(AnnotatedString(chars.toString(), text.spanStyles, text.paragraphStyles))
            marks.forEach { (from, to) -> addStyle(style, from, to) }
            // Two spaces are barely a step in a proportional font; widen them so depth reads.
            indents.forEach { (from, to) -> addStyle(INDENT, from, to) }
        }
        // One character for one, so the caret and selection map as they are.
        return TransformedText(shown, OffsetMapping.Identity)
    }

    override fun equals(other: Any?) = other is ListBulletTransformation && other.markColor == markColor
    override fun hashCode() = markColor.hashCode()
}
