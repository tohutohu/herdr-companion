package com.tohutohu.herdrcompanion.ui.markdown

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class CodeHighlightTest {
    private fun text(tokens: List<CodeToken>): String = tokens.joinToString("") { it.text }

    @Test
    fun `ハイライトしてもソース文字列を変更しない`() {
        val source = "fun main() {\n    println(\"hello\")\n}"

        assertEquals(source, text(highlightCode(source, "kotlin")))
    }

    @Test
    fun `Kotlinの主要なトークンを分類する`() {
        val tokens = highlightCode(
            "fun greet(name: String): Int { // comment\n    val count = 42\n    return \"hi\"\n}",
            "kotlin",
        )

        assertTrue(tokens.any { it.text == "fun" && it.kind == CodeTokenKind.Keyword })
        assertTrue(tokens.any { it.text == "greet" && it.kind == CodeTokenKind.Function })
        assertTrue(tokens.any { it.text == "String" && it.kind == CodeTokenKind.Type })
        assertTrue(tokens.any { it.text == "42" && it.kind == CodeTokenKind.Number })
        assertTrue(tokens.any { it.text == "// comment" && it.kind == CodeTokenKind.Comment })
        assertTrue(tokens.any { it.text == "\"hi\"" && it.kind == CodeTokenKind.String })
    }

    @Test
    fun `JSONのキーと値を分類する`() {
        val tokens = highlightCode("{\"name\": \"Herdr\", \"enabled\": true}", "json")

        assertTrue(tokens.any { it.text == "\"name\"" && it.kind == CodeTokenKind.Property })
        assertTrue(tokens.any { it.text == "\"Herdr\"" && it.kind == CodeTokenKind.String })
        assertTrue(tokens.any { it.text == "true" && it.kind == CodeTokenKind.Constant })
    }

    @Test
    fun `HTMLのタグ属性と文字列を分類する`() {
        val tokens = highlightCode("<button class=\"primary\">Run</button>", "html")

        assertTrue(tokens.any { it.text == "button" && it.kind == CodeTokenKind.Tag })
        assertTrue(tokens.any { it.text == "class" && it.kind == CodeTokenKind.Attribute })
        assertTrue(tokens.any { it.text == "\"primary\"" && it.kind == CodeTokenKind.String })
    }

    @Test
    fun `複数行コメントを行単位に分けても状態を保持する`() {
        val source = "/* first\nsecond */\nval answer = 42"
        val lines = highlightCodeLines(source, "kotlin")

        assertEquals(3, lines.size)
        assertTrue(lines[0].single { it.text == "/* first" }.kind == CodeTokenKind.Comment)
        assertTrue(lines[1].single { it.text == "second */" }.kind == CodeTokenKind.Comment)
        assertTrue(lines[2].any { it.text == "val" && it.kind == CodeTokenKind.Keyword })
        assertEquals(source, lines.joinToString("\n") { text(it) })
    }

    @Test
    fun `ファイル拡張子から主要言語を判定する`() {
        assertEquals("kotlin", languageForPath("android/app/src/MainActivity.kt"))
        assertEquals("typescript", languageForPath("web/src/App.tsx"))
        assertEquals("python", languageForPath("scripts/build.py"))
        assertEquals("json", languageForPath("config.json"))
        assertEquals("docker", languageForPath("Dockerfile"))
        assertEquals(null, languageForPath("README"))
    }
}
