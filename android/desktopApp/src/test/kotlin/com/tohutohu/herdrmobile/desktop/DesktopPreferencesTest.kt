package com.tohutohu.herdrmobile.desktop

import java.awt.Rectangle
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class DesktopPreferencesTest {
    @Test
    fun `window snapshot round trips and clamps unsafe dimensions`() {
        val original = DesktopWindowSnapshot(
            width = 10_000,
            height = 100,
            x = 12,
            y = 34,
            maximized = true,
        )

        val restored = DesktopWindowSnapshot.decode(original.encode())

        assertEquals(4_096, restored?.width)
        assertEquals(560, restored?.height)
        assertEquals(12, restored?.x)
        assertEquals(34, restored?.y)
        assertTrue(restored?.maximized == true)
    }

    @Test
    fun `window position is accepted when it intersects a current display`() {
        val display = Rectangle(0, 0, 1_440, 900)

        assertTrue(
            windowIntersectsAnyScreen(
                DesktopWindowSnapshot(width = 900, height = 600, x = 1_300, y = 500),
                listOf(display),
            ),
        )
        assertFalse(
            windowIntersectsAnyScreen(
                DesktopWindowSnapshot(width = 900, height = 600, x = 2_000, y = 500),
                listOf(display),
            ),
        )
    }
}
