package com.tohutohu.herdrcompanion.ui.files

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class FileViewerTest {
    @Test
    fun `Markdownの拡張子を判定する`() {
        assertTrue(isMarkdownFile("README.md"))
        assertTrue(isMarkdownFile("docs/guide.MARKDOWN"))
        assertTrue(isMarkdownFile("notes.mdown"))
    }

    @Test
    fun `Markdownではない拡張子を誤判定しない`() {
        assertFalse(isMarkdownFile("README.md.bak"))
        assertFalse(isMarkdownFile("src/Main.kt"))
        assertFalse(isMarkdownFile("Makefile"))
    }

    @Test
    fun `HTMLの拡張子を判定する`() {
        assertTrue(isHtmlFile("public/index.html"))
        assertTrue(isHtmlFile("docs/legacy.HTM"))
        assertFalse(isHtmlFile("index.html.bak"))
        assertFalse(isHtmlFile("src/Main.kt"))
    }
}
