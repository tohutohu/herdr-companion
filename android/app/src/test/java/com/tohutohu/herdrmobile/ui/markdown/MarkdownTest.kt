package com.tohutohu.herdrmobile.ui.markdown

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class MarkdownTest {
    private fun text(spans: List<MdSpan>) = spans.joinToString("") { it.text }

    @Test
    fun `見出しはレベルと本文に分解する`() {
        val blocks = parseMarkdown("## 手順\n本文")
        val heading = blocks[0] as MdBlock.Heading
        assertEquals(2, heading.level)
        assertEquals("手順", text(heading.spans))
        assertEquals("本文", text((blocks[1] as MdBlock.Paragraph).spans))
    }

    @Test
    fun `見出し記号のあとに空白がなければ見出しではない`() {
        assertTrue(parseMarkdown("#hashtag")[0] is MdBlock.Paragraph)
        assertEquals("C#", text((parseMarkdown("# C#")[0] as MdBlock.Heading).spans))
    }

    @Test
    fun `段落内の改行は保持する`() {
        val blocks = parseMarkdown("一行目\n二行目\n\n次の段落")
        assertEquals(2, blocks.size)
        assertEquals("一行目\n二行目", text((blocks[0] as MdBlock.Paragraph).spans))
    }

    @Test
    fun `箇条書きはインデントの深さと記号を持つ`() {
        val blocks = parseMarkdown("- 親\n  - 子\n    - 孫\n- 親2")
        val items = blocks.map { it as MdBlock.ListItem }
        assertEquals(listOf(0, 1, 2, 0), items.map { it.depth })
        assertEquals(listOf("•", "•", "•", "•"), items.map { it.marker })
        assertEquals("孫", text(items[2].spans))
    }

    @Test
    fun `番号付きリストは番号を記号として使う`() {
        val items = parseMarkdown("1. 最初\n2) 次").map { it as MdBlock.ListItem }
        assertEquals(listOf("1.", "2."), items.map { it.marker })
    }

    @Test
    fun `チェックボックスは記号に置き換える`() {
        val items = parseMarkdown("- [ ] 未完了\n- [x] 完了").map { it as MdBlock.ListItem }
        assertEquals(listOf("☐", "☑"), items.map { it.marker })
        assertEquals("未完了", text(items[0].spans))
    }

    @Test
    fun `リスト項目の継続行は同じ項目にまとめる`() {
        val items = parseMarkdown("- 一行目\n  続き\n- 次").map { it as MdBlock.ListItem }
        assertEquals(2, items.size)
        assertEquals("一行目\n続き", text(items[0].spans))
    }

    @Test
    fun `フェンスされたコードブロックは言語と中身を取り出す`() {
        val block = parseMarkdown("説明\n\n```kotlin\nval a = 1\n\n  val b = 2\n```\n後書き")[1] as MdBlock.CodeBlock
        assertEquals("kotlin", block.language)
        assertEquals("val a = 1\n\n  val b = 2", block.code)
    }

    @Test
    fun `コードブロック内のMarkdownは解釈しない`() {
        val block = parseMarkdown("```\n# not a heading\n- not a list\n```")[0] as MdBlock.CodeBlock
        assertEquals("# not a heading\n- not a list", block.code)
        assertEquals(null, block.language)
    }

    @Test
    fun `引用は入れ子のブロックとして解析する`() {
        val quote = parseMarkdown("> ## 見出し\n> 本文")[0] as MdBlock.Quote
        assertEquals(2, quote.blocks.size)
        assertEquals(2, (quote.blocks[0] as MdBlock.Heading).level)
    }

    @Test
    fun `水平線を認識する`() {
        assertTrue(parseMarkdown("上\n\n---\n\n下")[1] is MdBlock.Rule)
        assertTrue(parseMarkdown("***")[0] is MdBlock.Rule)
    }

    @Test
    fun `強調と取り消し線を解釈する`() {
        val spans = parseInline("**太字**と*斜体*と~~取り消し~~")
        assertEquals("太字", spans[0].text)
        assertTrue(spans[0].bold)
        assertTrue(spans.first { it.text == "斜体" }.italic)
        assertTrue(spans.first { it.text == "取り消し" }.strike)
        assertEquals("太字と斜体と取り消し", text(spans))
    }

    @Test
    fun `入れ子の強調は両方の装飾を適用する`() {
        val spans = parseInline("**太い*斜め*字**")
        val nested = spans.first { it.text == "斜め" }
        assertTrue(nested.bold && nested.italic)
        assertEquals("太い斜め字", text(spans))
    }

    @Test
    fun `インラインコードの中では他の記法を解釈しない`() {
        val spans = parseInline("`a_b*c*`を見る")
        assertTrue(spans[0].code)
        assertEquals("a_b*c*", spans[0].text)
    }

    @Test
    fun `単語中のアンダースコアは強調にしない`() {
        val spans = parseInline("snake_case_name はそのまま")
        assertEquals(1, spans.size)
        assertEquals("snake_case_name はそのまま", spans[0].text)
    }

    @Test
    fun `閉じていない記号はそのまま表示する`() {
        assertEquals("2 * 3 * 4", text(parseInline("2 * 3 * 4")))
        assertEquals("**未完", text(parseInline("**未完")))
        assertEquals("`未完", text(parseInline("`未完")))
    }

    @Test
    fun `エスケープした記号は文字として扱う`() {
        val spans = parseInline("""\*太字ではない\*""")
        assertEquals("*太字ではない*", text(spans))
        assertTrue(spans.none { it.bold })
    }

    @Test
    fun `リンクはURLを持つ`() {
        val spans = parseInline("詳細は[ドキュメント](https://example.com/a_b)を参照")
        val link = spans.first { it.text == "ドキュメント" }
        assertEquals("https://example.com/a_b", link.link)
        assertEquals("詳細はドキュメントを参照", text(spans))
    }

    @Test
    fun `http以外のスキームのリンクはラベルだけを表示して開かない`() {
        val spans = parseInline("[設定](intent://x#Intent;scheme=foo;end) と [電話](tel:123)")
        assertTrue(spans.none { it.link != null })
        assertEquals("設定 と 電話", text(spans))
    }

    @Test
    fun `裸のURLもリンクにする`() {
        val spans = parseInline("見て: https://example.com/x 。")
        val link = spans.first { it.link != null }
        assertEquals("https://example.com/x", link.link)
        assertEquals("見て: https://example.com/x 。", text(spans))
    }

    @Test
    fun `装飾のない文章は1つの段落になる`() {
        val blocks = parseMarkdown("方針を固めるため、確認します。")
        val spans = (blocks.single() as MdBlock.Paragraph).spans
        assertEquals(1, spans.size)
        assertEquals("方針を固めるため、確認します。", spans[0].text)
    }
}
