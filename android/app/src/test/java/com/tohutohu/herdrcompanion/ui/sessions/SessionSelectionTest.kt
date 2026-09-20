package com.tohutohu.herdrcompanion.ui.sessions

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class SessionSelectionTest {
    @Test
    fun `選択がないときは選択モードではない`() {
        val selection = SessionSelection()
        assertFalse(selection.active)
    }

    @Test
    fun `トグルで選択と解除を切り替える`() {
        val selection = SessionSelection()
        selection.toggle("a")
        assertTrue(selection.active)
        assertTrue(selection.contains("a"))

        selection.toggle("b")
        assertEquals(setOf("a", "b"), selection.ids)

        selection.toggle("a")
        assertEquals(setOf("b"), selection.ids)

        selection.toggle("b")
        assertFalse(selection.active)
    }

    @Test
    fun `すべて選択は一覧のセッションを選ぶ`() {
        val selection = SessionSelection()
        selection.toggle("a")
        selection.selectAll(listOf("a", "b", "c"))
        assertEquals(setOf("a", "b", "c"), selection.ids)
    }

    @Test
    fun `一覧から消えたセッションは選択から外れる`() {
        val selection = SessionSelection()
        selection.selectAll(listOf("a", "b", "c"))
        selection.keepOnly(listOf("b", "c", "d"))
        assertEquals(setOf("b", "c"), selection.ids)
    }

    @Test
    fun `クリアで選択モードを抜ける`() {
        val selection = SessionSelection()
        selection.selectAll(listOf("a", "b"))
        selection.clear()
        assertFalse(selection.active)
        assertEquals(emptySet<String>(), selection.ids)
    }
}
