package com.tohutohu.herdrmobile.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ContextLabelTest {
    @Test
    fun `トークン数は千区切りの短い表記になる`() {
        assertEquals("999", tokenCount(999))
        assertEquals("1k", tokenCount(1_000))
        assertEquals("154k", tokenCount(154_321))
        assertEquals("1M", tokenCount(1_000_000))
        assertEquals("1.5M", tokenCount(1_500_000))
    }

    @Test
    fun `コンテキストの使用量は使用分と窓の広さで表す`() {
        assertEquals("154k / 1M", contextFill(154_000, 1_000_000))
        assertEquals("64k / 258k", contextFill(64_600, 258_400))
    }

    @Test
    fun `報告のないセッションは使用量を表示しない`() {
        assertNull(contextFill(null, 1_000_000))
        assertNull(contextFill(154_000, null))
        assertNull(contextFill(154_000, 0))
        assertNull(contextLabel(null))
        assertEquals("ctx 15%", contextLabel(15))
    }
}
