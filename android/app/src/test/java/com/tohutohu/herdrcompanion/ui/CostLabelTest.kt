package com.tohutohu.herdrcompanion.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class CostLabelTest {
    @Test
    fun `コストはドル表記で見積もりには波線をつける`() {
        assertEquals("~$2.21", costLabel(2.2070145, true))
        assertEquals("$2.21", costLabel(2.2070145, false))
        assertEquals("$0.01", costLabel(0.01, false))
        assertEquals("~$123", costLabel(123.456, true))
    }

    @Test
    fun `1セント未満は0ドルではなく未満として表す`() {
        assertEquals("~<$0.01", costLabel(0.0004, true))
        assertEquals("<$0.01", costLabel(0.009, false))
    }

    @Test
    fun `報告のないセッションはコストを表示しない`() {
        assertNull(costLabel(null, true))
        assertNull(costLabel(0.0, true))
    }
}
