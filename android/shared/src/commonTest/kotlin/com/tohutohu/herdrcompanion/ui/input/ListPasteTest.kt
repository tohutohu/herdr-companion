package com.tohutohu.herdrcompanion.ui.input

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class ListPasteTest {
    @Test
    fun 中黒や記号の箇条書きをインデントの深さごとに入れ子にする() {
        assertEquals("- 一\n- 二", normalizePastedList("・一\n・二"))
        assertEquals("- 一\n  - 二\n    - 三\n- 四", normalizePastedList("\t•\t一\n\t\t◦\t二\n\t\t\t▪\t三\n\t•\t四"))
    }

    @Test
    fun 記号だけの行は次の行の本文とつなぐ() {
        assertEquals("- 一\n- 二", normalizePastedList("•\n一\n•\n二"))
    }

    @Test
    fun 会話からコピーした箇条書きは記号の種類で深さを戻す() {
        // MarkdownText puts each bullet in its own Text, so a copied selection has it on its own line.
        assertEquals("- 一\n  - 子\n    - 孫\n- 二", normalizePastedList("•\n一\n◦\n子\n‣\n孫\n•\n二"))
        assertEquals("1. 手順\n   - 詳細\n2. 次", normalizePastedList("1.\n手順\n◦\n詳細\n2.\n次"))
        assertEquals("- [ ] 未\n- [x] 済み", normalizePastedList("☐\n未\n☑\n済み"))
    }

    @Test
    fun タブ区切りの番号付きは番号付きの項目にする() {
        assertEquals("1. 一\n   - 子\n2. 二", normalizePastedList("1.\t一\n\t•\t子\n2.\t二"))
    }

    @Test
    fun 箇条書きのない文章やMarkdownはそのまま() {
        assertEquals("ふつうの文\n- 既に\n  - Markdown", normalizePastedList("ふつうの文\n- 既に\n  - Markdown"))
        assertEquals("1. 一\n2. 二", normalizePastedList("1. 一\n2. 二"))
    }

    @Test
    fun HTMLの入れ子のリストをMarkdownにする() {
        val html = """
            <meta charset='utf-8'><p>前置き</p>
            <ul>
              <li>一
                <ul><li>子<ol><li>孫A</li><li>孫B</li></ol></li></ul>
              </li>
              <li><p>二</p><p>二の続き</p></li>
            </ul>
            <ol start="3"><li>三</li><li>四 &amp; 五</li></ol>
        """.trimIndent()
        val expected = """
            前置き

            - 一
              - 子
                1. 孫A
                2. 孫B
            - 二
              二の続き

            3. 三
            4. 四 & 五
        """.trimIndent()
        assertEquals(expected, htmlToListText(html))
    }

    @Test
    fun GoogleドキュメントのariaLevelとチェックボックスを読む() {
        val html = """<ul><li aria-level="1"><p><span>一</span></p></li><li aria-level="2"><p><span>二</span></p></li></ul>""" +
            """<ul><li><input type="checkbox" checked>済み</li><li><input type="checkbox">未</li></ul>"""
        assertEquals("- 一\n  - 二\n\n- [x] 済み\n- [ ] 未", htmlToListText(html))
    }

    @Test
    fun 閉じタグのないliは次のliで閉じる() {
        assertEquals("- 一\n- 二", htmlToListText("<ul><li>一<li>二</ul>"))
    }

    @Test
    fun 本文の消えたプレーンテキストの代わりにHTMLのリストを使う() {
        val html = "<ul><li>一<ul><li>二</li></ul></li><li>三</li></ul>"
        assertEquals("- 一\n  - 二\n- 三", listTextForPaste(html, "一\n二\n三"))
    }

    @Test
    fun プレーンテキストに箇条書きが残っていればそちらを使う() {
        val html = "<ul><li><b>一</b></li><li>二</li></ul>"
        assertNull(listTextForPaste(html, "- **一**\n- 二"))
        assertNull(listTextForPaste(html, "• 一\n• 二"))
    }

    @Test
    fun HTMLとプレーンテキストの中身が違えばHTMLを使わない() {
        assertNull(listTextForPaste("<ul><li>ã\u0081\u0082</li></ul>", "あ"))
        assertNull(listTextForPaste("<p>リストなし</p>", "リストなし"))
    }
}
