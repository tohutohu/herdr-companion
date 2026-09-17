package com.tohutohu.herdrmobile.ui.detail

import org.junit.Assert.assertEquals
import org.junit.Test

class MessageStartOffsetTest {
    @Test
    fun `画面に収まるメッセージは末尾のまま動かさない`() {
        assertEquals(0, messageStartOffset(messageHeight = 400, viewportHeight = 1800, panelHeight = 0))
    }

    @Test
    fun `画面より長いメッセージは先頭が上端に来るまで押し出す`() {
        assertEquals(700, messageStartOffset(messageHeight = 2500, viewportHeight = 1800, panelHeight = 0))
    }

    @Test
    fun `固定パネルの高さだけ余分に押し出して先頭を隠さない`() {
        assertEquals(900, messageStartOffset(messageHeight = 2500, viewportHeight = 1800, panelHeight = 200))
    }

    @Test
    fun `パネルの下に収まるメッセージも末尾のまま動かさない`() {
        assertEquals(0, messageStartOffset(messageHeight = 1600, viewportHeight = 1800, panelHeight = 200))
    }
}
