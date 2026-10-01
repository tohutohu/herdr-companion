package com.tohutohu.herdrcompanion.ui

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class PlacementPauseTest {
    private data class Item(val id: String, val text: String)

    private fun changed(before: List<Item>, after: List<Item>) = changedInPlace(before, after) { it.id }

    private val items = listOf(Item("a", "確認します。"), Item("b", "続けて"))

    @Test
    fun `既存の項目の中身が変わればその場での変化とみなす`() {
        assertTrue(changed(items, listOf(Item("a", "確認しました。\n2行目"), Item("b", "続けて"))))
    }

    @Test
    fun `追加や削除だけならその場での変化とみなさない`() {
        assertFalse(changed(items, items + Item("c", "はい")))
        assertFalse(changed(items, items.drop(1)))
        assertFalse(changed(emptyList(), items))
    }

    @Test
    fun `並び替わった時は位置のアニメーションに任せる`() {
        assertFalse(changed(items, listOf(Item("b", "続けて"), Item("a", "確認しました。"))))
    }
}
