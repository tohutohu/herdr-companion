package com.tohutohu.herdrcompanion.desktop

import kotlin.test.Test
import kotlin.test.assertEquals

class DesktopSessionOpeningTest {
    @Test
    fun `normal opening replaces the last focused pane`() {
        assertEquals(
            listOf("one", "new", "three"),
            openSessionInFocusedPane(
                openSessionIds = listOf("one", "two", "three"),
                focusedSessionId = "two",
                sessionId = "new",
            ),
        )
    }

    @Test
    fun `normal opening falls back to the last pane when focus is unavailable`() {
        assertEquals(
            listOf("one", "two", "new"),
            openSessionInFocusedPane(
                openSessionIds = listOf("one", "two", "three"),
                focusedSessionId = null,
                sessionId = "new",
            ),
        )
    }

    @Test
    fun `opening an already open session does not duplicate its pane`() {
        val open = listOf("one", "two")
        assertEquals(open, openSessionInFocusedPane(open, "one", "two"))
    }
}
