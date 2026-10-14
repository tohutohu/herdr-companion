package com.tohutohu.herdrcompanion.desktop

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class DesktopPaneLayoutTest {
    @Test
    fun `new panes start with equal weights`() {
        assertEquals(listOf(1f, 1f, 1f), equalPaneWeights(3))
        assertEquals(emptyList(), equalPaneWeights(0))
    }

    @Test
    fun `dragging a divider transfers width only between adjacent panes`() {
        val adjusted = adjustPaneDividerWeights(
            current = listOf(1f, 1f, 1f),
            dividerIndex = 1,
            deltaPx = 100f,
            paneWidthPx = 900f,
            minimumPaneWidthPx = 100f,
        )

        assertEquals(1f, adjusted[0])
        assertTrue(adjusted[1] > 1f)
        assertTrue(adjusted[2] < 1f)
        assertEquals(3f, adjusted.sum(), absoluteTolerance = 0.0001f)
    }

    @Test
    fun `divider drag respects the minimum width`() {
        val adjusted = adjustPaneDividerWeights(
            current = listOf(1f, 1f),
            dividerIndex = 0,
            deltaPx = -1_000f,
            paneWidthPx = 600f,
            minimumPaneWidthPx = 180f,
        )

        assertEquals(180f / 600f, adjusted[0] / adjusted.sum(), absoluteTolerance = 0.0001f)
    }

    @Test
    fun `ヘッダーを隣のペインの中央より先へドラッグすると入れ替わる`() {
        val target = paneDragTarget(
            widthsPx = listOf(300f, 300f, 300f),
            dividerWidthPx = 10f,
            fromIndex = 0,
            offsetPx = 200f,
        )

        assertEquals(1, target.index)
        // 移動後の位置 310px から見て、ポインタは元の 200px 地点に留まる
        assertEquals(-110f, target.offsetPx, absoluteTolerance = 0.0001f)
    }

    @Test
    fun `隣のペインの中央に届かないドラッグでは並びが変わらない`() {
        val target = paneDragTarget(
            widthsPx = listOf(300f, 300f),
            dividerWidthPx = 10f,
            fromIndex = 1,
            offsetPx = -100f,
        )

        assertEquals(PaneDragTarget(1, -100f), target)
    }

    @Test
    fun `大きく左へドラッグすると複数のペインを越えて先頭に移る`() {
        val target = paneDragTarget(
            widthsPx = listOf(200f, 200f, 400f),
            dividerWidthPx = 0f,
            fromIndex = 2,
            offsetPx = -380f,
        )

        assertEquals(0, target.index)
        assertEquals(20f, target.offsetPx, absoluteTolerance = 0.0001f)
    }

    @Test
    fun `ドラッグ中のペインは分割領域の外へ出ない`() {
        val target = paneDragTarget(
            widthsPx = listOf(300f, 300f),
            dividerWidthPx = 10f,
            fromIndex = 0,
            offsetPx = -500f,
        )

        assertEquals(0, target.index)
        assertEquals(0f, target.offsetPx, absoluteTolerance = 0.0001f)
    }

    @Test
    fun `幅の違うペインを入れ替えた直後に元へ戻らない`() {
        val widths = listOf(600f, 200f)
        val swapped = paneDragTarget(widths, dividerWidthPx = 8f, fromIndex = 0, offsetPx = 410f)
        assertEquals(1, swapped.index)

        val next = paneDragTarget(widths.moved(0, 1), dividerWidthPx = 8f, fromIndex = 1, offsetPx = swapped.offsetPx)
        assertEquals(1, next.index)
    }

    @Test
    fun `ペインの並べ替えは要素を指定位置へ移す`() {
        assertEquals(listOf("b", "c", "a"), listOf("a", "b", "c").moved(0, 2))
        assertEquals(listOf("c", "a", "b"), listOf("a", "b", "c").moved(2, 0))
    }
}
