package com.tohutohu.herdrcompanion.ui.sessions

import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class SessionRowSelectionTest {
    @Test
    fun 選択中は開いているペインのハイライトをチェック表示にしない() {
        assertFalse(rowShownSelected(highlighted = true, selecting = true, checked = false))
    }

    @Test
    fun 選択中はチェックしたセッションだけを選択表示にする() {
        assertTrue(rowShownSelected(highlighted = false, selecting = true, checked = true))
    }

    @Test
    fun 選択していないときは開いているペインをハイライトする() {
        assertTrue(rowShownSelected(highlighted = true, selecting = false, checked = false))
        assertFalse(rowShownSelected(highlighted = false, selecting = false, checked = false))
    }
}
