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
}
