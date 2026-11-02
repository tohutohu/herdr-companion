package com.tohutohu.herdrcompanion.ui.files

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class HtmlCommentsTest {
    private val title = ElementPick("body > section:nth-of-type(2) > h2", "<h2>", "今週のトークン数", canParent = true)
    private val logo = ElementPick("#logo", "<img id=\"logo\" src=\"img/logo.svg\">")

    @Test
    fun `ページの script が送る要素の説明を読む`() {
        val json = """{"selector":"#logo","tag":"<img id=\"logo\" src=\"img/logo.svg\">","text":"","canParent":false,"extra":1}"""
        assertEquals(logo, parseElementPick(json))
        assertNull(parseElementPick("not json"))
    }

    @Test
    fun `コメントをページごとにまとめ通し番号で要素と一緒に並べる`() {
        val text = formatPageComments(
            listOf(
                PageComment("/w/report/index.html", title, "もっと短く\n例: 週間トークン"),
                PageComment("/w/report/details.html", logo, "差し替えたい"),
                PageComment("/w/report/index.html", logo, " 小さく "),
            ),
        )
        assertEquals(
            """
            Comments on the page:

            /w/report/index.html
            1. `body > section:nth-of-type(2) > h2` <h2> "今週のトークン数"
               もっと短く
               例: 週間トークン
            2. `#logo` <img id="logo" src="img/logo.svg">
               小さく

            /w/report/details.html
            3. `#logo` <img id="logo" src="img/logo.svg">
               差し替えたい
            """.trimIndent(),
            text,
        )
    }

    @Test
    fun `表示中のページのコメントだけを一覧の番号のまま印にする`() {
        val comments = listOf(
            PageComment("/w/a.html", title, "x"),
            PageComment("/w/b.html", logo, "y"),
            PageComment("/w/a.html", logo, "z"),
        )
        assertEquals(
            """[{"s":"body > section:nth-of-type(2) > h2","n":1},{"s":"#logo","n":3}]""",
            commentMarksJson(comments, "/w/a.html"),
        )
    }

    @Test
    fun `書きかけのコメントはセッションとファイルごとに残り空にすると消える`() {
        val comments = listOf(PageComment("/w/a.html", title, "x"))
        PendingPageComments["claude:a", "/w/a.html"] = comments
        assertEquals(comments, PendingPageComments["claude:a", "/w/a.html"])
        assertEquals(emptyList<PageComment>(), PendingPageComments["claude:b", "/w/a.html"])
        PendingPageComments["claude:a", "/w/a.html"] = emptyList()
        assertEquals(emptyList<PageComment>(), PendingPageComments["claude:a", "/w/a.html"])
    }
}
