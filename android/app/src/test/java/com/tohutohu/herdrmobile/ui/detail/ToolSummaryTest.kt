package com.tohutohu.herdrmobile.ui.detail

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ToolSummaryTest {
    @Test
    fun `説明付きのBashコマンドはツール呼び出しと判定する`() {
        assertTrue(isToolCallText("Check gateway\n$ cd gateway && go test ./..."))
        assertTrue(isToolCallText("$ ls"))
    }

    @Test
    fun `1行のファイル操作や検索はツール呼び出しと判定する`() {
        assertTrue(isToolCallText("Read android/app/build.gradle.kts"))
        assertTrue(isToolCallText("▸ ToolSearch {\"query\":\"x\"}"))
    }

    @Test
    fun `通常の文章はツール呼び出しと判定しない`() {
        assertFalse(isToolCallText("方針を固めるため、確認します。"))
        assertFalse(isToolCallText("Read the docs first.\nThen continue."))
        assertFalse(isToolCallText("手順:\n1. ビルド\n$ は不要"))
    }

    @Test
    fun `折りたたみ時は1行目と行数を表示する`() {
        assertEquals("error: x  (3 lines)", toolSummary("error: x\nat a\nat b\n"))
        assertEquals("Read a.kt", toolSummary("Read a.kt"))
    }
}
