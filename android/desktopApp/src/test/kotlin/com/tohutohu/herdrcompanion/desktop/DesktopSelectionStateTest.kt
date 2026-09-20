package com.tohutohu.herdrcompanion.desktop

import kotlin.test.Test
import kotlin.test.assertEquals

class DesktopSelectionStateTest {
    @Test
    fun `selection replaces the detail subscription target`() {
        val selection = DesktopSelectionState()

        selection.select("claude:one")
        selection.select("codex:two")

        assertEquals("codex:two", selection.selectedId)
        selection.clear()
        assertEquals(null, selection.selectedId)
    }
}
