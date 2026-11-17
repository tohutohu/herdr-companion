package com.tohutohu.herdrcompanion.ui.input

import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class ListEditingTest {
    /** "|" marks the caret, or "[" and "]" a selection. */
    private fun v(marked: String): TextFieldValue {
        val caret = marked.indexOf('|')
        if (caret >= 0) return TextFieldValue(marked.removeRange(caret, caret + 1), TextRange(caret))
        val start = marked.indexOf('[')
        val end = marked.indexOf(']') - 1
        return TextFieldValue(marked.replace("[", "").replace("]", ""), TextRange(start, end))
    }

    private fun shown(value: TextFieldValue?): String? {
        value ?: return null
        val s = value.selection
        if (s.collapsed) return value.text.substring(0, s.start) + "|" + value.text.substring(s.start)
        return value.text.substring(0, s.min) + "[" + value.text.substring(s.min, s.max) + "]" + value.text.substring(s.max)
    }

    @Test
    fun 項目の末尾で改行すると同じ深さの項目が続く() {
        assertEquals("- 一\n- |", shown(continueList(v("- 一|"))))
        assertEquals("  * 一\n  * |", shown(continueList(v("  * 一|"))))
    }

    @Test
    fun 項目の途中で改行すると後ろの文字は次の項目に移る() {
        assertEquals("- 一\n- |二", shown(continueList(v("- 一|二"))))
    }

    @Test
    fun 番号付きの項目は番号を進め後続の番号も振り直す() {
        assertEquals("1. 一\n2. |\n3. 二\n4. 三", shown(continueList(v("1. 一|\n2. 二\n3. 三"))))
        assertEquals("1) 一\n2) |", shown(continueList(v("1) 一|"))))
    }

    @Test
    fun 入れ子の項目と続きの行は振り直しで飛ばす() {
        val before = "1. 一|\n   - 子\n     続き\n2. 二\n\n1. 別のリスト"
        assertEquals("1. 一\n2. |\n   - 子\n     続き\n3. 二\n\n1. 別のリスト", shown(continueList(v(before))))
    }

    @Test
    fun チェックボックスは空のチェックボックスで続く() {
        assertEquals("- [x] 済み\n- [ ] |", shown(continueList(v("- [x] 済み|"))))
    }

    @Test
    fun 空の項目で改行すると一段浅くなり最上位ならリストを抜ける() {
        assertEquals("- 一\n- |", shown(continueList(v("- 一\n  - |"))))
        assertEquals("- 一\n|", shown(continueList(v("- 一\n- |"))))
    }

    @Test
    fun 項目でない行やマーカーの前では何もしない() {
        assertNull(continueList(v("ふつうの行|")))
        assertNull(continueList(v("|- 一")))
        assertNull(continueList(v("-|")))
        assertNull(continueList(v("**太字**|")))
    }

    @Test
    fun Shift改行は項目の本文の位置に揃えた行を足す() {
        assertEquals("1. 一\n   |", shown(breakInsideListItem(v("1. 一|"))))
    }

    @Test
    fun Tabで上の項目の下に入れ子にする() {
        assertEquals("- 一\n  - |二", shown(indentListItems(v("- 一\n- |二"), outdent = false)))
        assertEquals("1. 一\n   1. |二", shown(indentListItems(v("1. 一\n2. |二"), outdent = false)))
    }

    @Test
    fun ShiftTabで一段浅くし番号は兄弟に続ける() {
        assertEquals("1. 一\n2. |二", shown(indentListItems(v("1. 一\n   1. |二"), outdent = true)))
        assertEquals("- 一\n  - 二\n  - |三", shown(indentListItems(v("- 一\n  - 二\n    - |三"), outdent = true)))
        assertNull(indentListItems(v("ふつう|"), outdent = false))
        assertEquals("- |一", shown(indentListItems(v("- |一"), outdent = true)))
    }

    @Test
    fun 選択した複数の項目をまとめて入れ子にする() {
        val result = indentListItems(v("- 一\n- [二\n- 三]"), outdent = false)
        assertEquals("- 一\n  - [二\n  - 三]", shown(result))
    }

    @Test
    fun ソフトキーボードの改行もリストを続ける() {
        val old = v("- 一|")
        val typed = TextFieldValue("- 一\n", TextRange(4))
        assertEquals("- 一\n- |", shown(applyListTyping(old, typed)))
    }

    @Test
    fun リスト以外の入力はそのまま通す() {
        val old = v("あ|")
        val typed = TextFieldValue("あい", TextRange(2))
        assertEquals(typed, applyListTyping(old, typed))
        val newline = TextFieldValue("あ\n", TextRange(2))
        assertEquals(newline, applyListTyping(old, newline))
    }

    @Test
    fun 記号の箇条書きを貼るとMarkdownの項目になる() {
        val old = v("|")
        val pasted = "• 一\n\t◦\t二\n• 三"
        val result = applyListTyping(old, TextFieldValue(pasted, TextRange(pasted.length)))
        assertEquals("- 一\n  - 二\n- 三|", shown(result))
    }

    @Test
    fun 空の項目に貼るとその深さに入る() {
        val old = v("- 一\n  - |")
        val pasted = "- 二\n  - 三"
        val typed = TextFieldValue(old.text + pasted, TextRange(old.text.length + pasted.length))
        assertEquals("- 一\n  - 二\n    - 三|", shown(applyListTyping(old, typed)))
    }
}
