package com.tohutohu.herdrcompanion.ui.files

import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import org.junit.Assert.assertEquals
import org.junit.Test

class ImageZoomTest {
    private val bounds = Size(1000f, 2000f)

    /** Where a point of the unzoomed layout ends up on screen. */
    private fun ImageZoom.project(point: Offset): Offset {
        val centre = Offset(bounds.width / 2, bounds.height / 2)
        return centre + offset + (point - centre) * scale
    }

    @Test
    fun `ピンチの中心にある点はズームしても動かない`() {
        val pinch = Offset(800f, 1200f)
        val zoomed = ImageZoom().zoomed(2f, pinch, bounds, bounds)
        assertEquals(2f, zoomed.scale)
        assertEquals(pinch, zoomed.project(pinch))
        val again = zoomed.zoomed(1.5f, Offset(300f, 700f), bounds, bounds)
        val before = Offset(300f, 700f)
        // The layout point under the second pinch stays under it.
        val layoutPoint = Offset(bounds.width / 2, bounds.height / 2) + (before - Offset(bounds.width / 2, bounds.height / 2) - zoomed.offset) / zoomed.scale
        assertEquals(before.x, again.project(layoutPoint).x, 0.01f)
        assertEquals(before.y, again.project(layoutPoint).y, 0.01f)
    }

    @Test
    fun `倍率は1倍から8倍に収まり、等倍に戻ると位置もリセットされる`() {
        val zoomed = ImageZoom().zoomed(20f, Offset(100f, 100f), bounds, bounds)
        assertEquals(ImageZoom.MAX_SCALE, zoomed.scale)
        assertEquals(ImageZoom(), zoomed.zoomed(0.01f, Offset(900f, 1900f), bounds, bounds))
    }

    @Test
    fun `ズーム中のパンは画像の端が画面の内側に入るところで止まる`() {
        val zoomed = ImageZoom(scale = 2f)
        val panned = zoomed.panned(Offset(5000f, -5000f), bounds, bounds)
        assertEquals(Offset(500f, -1000f), panned.offset)
    }

    @Test
    fun `画面より細い画像は横にはパンできない`() {
        // A square picture fitted into a tall screen is 1000x1000; at 2x it's 2000x2000.
        val content = fittedSize(Size(400f, 400f), bounds)
        assertEquals(Size(1000f, 1000f), content)
        val panned = ImageZoom(scale = 1.5f).panned(Offset(300f, 300f), bounds, content)
        // 1500 wide overflows by 250 per side; 1500 tall still fits inside 2000.
        assertEquals(Offset(250f, 0f), panned.offset)
    }

    @Test
    fun `大きさがわからない画像は表示領域いっぱいとみなす`() {
        assertEquals(bounds, fittedSize(Size.Unspecified, bounds))
        assertEquals(bounds, fittedSize(Size(0f, 0f), bounds))
    }
}
