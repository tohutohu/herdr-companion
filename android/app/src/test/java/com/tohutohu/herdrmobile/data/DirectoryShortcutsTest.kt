package com.tohutohu.herdrmobile.data

import org.junit.Assert.assertEquals
import org.junit.Test

class DirectoryShortcutsTest {
    @Test
    fun `最近使ったディレクトリは先頭に追加され重複は取り除かれる`() {
        assertEquals(listOf("/b", "/a", "/c"), pushRecent(listOf("/a", "/b", "/c"), "/b"))
        assertEquals(listOf("/new"), pushRecent(emptyList(), "/new"))
    }

    @Test
    fun `最近使ったディレクトリは上限を超えた古いものから捨てられる`() {
        assertEquals(listOf("/d", "/a", "/b"), pushRecent(listOf("/a", "/b", "/c"), "/d", max = 3))
    }

    @Test
    fun `最後に使ったディレクトリはお気に入りでも先頭から復元する`() {
        val shortcuts = DirectoryShortcuts(
            favorites = listOf("/workspace/app"),
            recents = listOf("/workspace/app", "/workspace/other"),
        )
        assertEquals("/workspace/app", lastUsedDirectory(shortcuts))
    }

    @Test
    fun `お気に入りは未登録なら追加されパス順に並ぶ`() {
        assertEquals(listOf("/a", "/b", "/c"), toggleFavorite(listOf("/a", "/c"), "/b"))
    }

    @Test
    fun `お気に入りは登録済みなら外れる`() {
        assertEquals(listOf("/a"), toggleFavorite(listOf("/a", "/b"), "/b"))
    }
}
