package com.tohutohu.herdrcompanion.desktop

import java.awt.Point
import java.awt.Rectangle
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class DesktopTearOffTest {
    private val area = Rectangle(100, 50, 610, 800)
    private val widths = listOf(300f, 300f)

    private fun indexAt(x: Int, y: Int, paneWidths: List<Float> = widths) =
        paneDropIndex(Point(x, y), area, stripHeight = 40, paneWidths = paneWidths, dividerWidth = 10f)

    @Test
    fun `ヘッダー帯の上ではポインタに近いペインの境目に差し込む`() {
        assertEquals(0, indexAt(x = 150, y = 60))
        assertEquals(1, indexAt(x = 300, y = 60))
        assertEquals(1, indexAt(x = 500, y = 60))
        assertEquals(2, indexAt(x = 700, y = 60))
    }

    @Test
    fun `ヘッダー帯の外やメインウィンドウの外では戻さない`() {
        assertNull(indexAt(x = 300, y = 200))
        assertNull(indexAt(x = 50, y = 60))
        assertNull(indexAt(x = 300, y = 10))
    }

    @Test
    fun `ペインが無ければ分割領域のどこでも受け取る`() {
        assertEquals(0, indexAt(x = 300, y = 600, paneWidths = emptyList()))
        assertNull(indexAt(x = 300, y = 900, paneWidths = emptyList()))
    }
}
