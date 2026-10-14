package com.tohutohu.herdrcompanion.desktop

import java.awt.Point
import java.awt.Rectangle
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class DesktopTearOffTest {
    // 2 panes open: docking makes 3 equal slots of 300pt with 10pt dividers.
    private val area = Rectangle(100, 50, 920, 800)

    private fun previewAt(x: Int, y: Int, paneCount: Int = 2, grabFraction: Float = 0.5f) =
        dockPreview(Point(x, y), area, stripHeight = 40, paneCount = paneCount, dividerWidth = 10f, grabFraction = grabFraction)

    @Test
    fun `ヘッダー帯の上では掴んだ位置に最も近いスロットに入る`() {
        assertEquals(0, previewAt(x = 250, y = 60)?.index)
        assertEquals(1, previewAt(x = 560, y = 60)?.index)
        assertEquals(2, previewAt(x = 870, y = 60)?.index)
    }

    @Test
    fun `プレビューのペインはポインタに付いてスロットからずれる`() {
        // Slot 1 starts at 310; held at its middle 40pt to the right of the slot centre.
        assertEquals(DesktopDockPreview(1, 40f), previewAt(x = 100 + 310 + 150 + 40, y = 60))
        // Held by its left quarter, the same pointer puts the pane further right.
        assertEquals(DesktopDockPreview(1, 115f), previewAt(x = 100 + 310 + 150 + 40, y = 60, grabFraction = 0.25f))
    }

    @Test
    fun `プレビューのペインは分割領域の外へはみ出さない`() {
        assertEquals(DesktopDockPreview(0, 0f), previewAt(x = 101, y = 60))
        assertEquals(DesktopDockPreview(2, 0f), previewAt(x = 1019, y = 60))
    }

    @Test
    fun `ヘッダー帯の外やメインウィンドウの外では戻さない`() {
        assertNull(previewAt(x = 300, y = 200))
        assertNull(previewAt(x = 50, y = 60))
        assertNull(previewAt(x = 300, y = 10))
    }

    @Test
    fun `ペインが無ければ分割領域のどこでも全幅で受け取る`() {
        assertEquals(DesktopDockPreview(0, 0f), previewAt(x = 300, y = 600, paneCount = 0))
        assertNull(previewAt(x = 300, y = 900, paneCount = 0))
    }
}
