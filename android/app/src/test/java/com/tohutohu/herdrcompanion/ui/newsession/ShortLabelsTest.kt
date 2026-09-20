package com.tohutohu.herdrcompanion.ui.newsession

import org.junit.Assert.assertEquals
import org.junit.Test

class ShortLabelsTest {
    @Test
    fun `ラベルはフォルダ名だけになる`() {
        assertEquals(
            mapOf("/Users/me/workspace/app" to "app", "/Users/me/notes/" to "notes"),
            shortLabels(listOf("/Users/me/workspace/app", "/Users/me/notes/")),
        )
    }

    @Test
    fun `同じフォルダ名があれば親フォルダ名を付けて区別する`() {
        assertEquals(
            mapOf("/w/a/app" to "a/app", "/w/b/app" to "b/app", "/w/web" to "web"),
            shortLabels(listOf("/w/a/app", "/w/b/app", "/w/web")),
        )
    }

    @Test
    fun `ルートはスラッシュで表す`() {
        assertEquals(mapOf("/" to "/"), shortLabels(listOf("/")))
    }
}
