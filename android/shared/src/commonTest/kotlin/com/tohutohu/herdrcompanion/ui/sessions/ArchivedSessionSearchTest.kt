package com.tohutohu.herdrcompanion.ui.sessions

import com.tohutohu.herdrcompanion.data.api.SessionDto
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue
import kotlinx.coroutines.runBlocking

class ArchivedSessionSearchTest {
    private val hit = SessionDto(id = "claude:s1", provider = "claude", status = "offline", updatedAt = "")

    @Test
    fun `Gatewayの検索結果を入力した語に対してだけ返す`() = runBlocking {
        val asked = mutableListOf<String>()
        val search = ArchivedSessionSearch(debounceMillis = 0) { asked += it; listOf(hit) }

        assertNull(search.resultsFor("login"))
        search.search(" login ")

        assertEquals(listOf("login"), asked)
        assertEquals(listOf(hit), search.resultsFor("login"))
        assertNull(search.resultsFor("logi"), "別の語の結果としては使わない")
        assertFalse(search.needsAllPages("login"))
    }

    @Test
    fun `語が空なら問い合わせず結果を捨てる`() = runBlocking {
        val asked = mutableListOf<String>()
        val search = ArchivedSessionSearch(debounceMillis = 0) { asked += it; listOf(hit) }
        search.search("login")

        search.search("  ")

        assertEquals(listOf("login"), asked)
        assertNull(search.resultsFor("login"))
        assertNull(search.resultsFor(""))
    }

    @Test
    fun `検索のないGatewayでは全ページの読み込みに任せ以後は問い合わせない`() = runBlocking {
        var asked = 0
        val search = ArchivedSessionSearch(debounceMillis = 0) { asked++; null }

        search.search("login")
        search.search("signup")

        assertEquals(1, asked)
        assertNull(search.resultsFor("signup"))
        assertTrue(search.needsAllPages("signup"))
    }

    @Test
    fun `検索に失敗した語だけ全ページの読み込みに任せる`() = runBlocking {
        var fail = true
        val search = ArchivedSessionSearch(debounceMillis = 0) { if (fail) error("offline") else listOf(hit) }

        search.search("login")
        assertTrue(search.needsAllPages("login"))
        assertNull(search.resultsFor("login"))

        fail = false
        search.search("signup")
        assertFalse(search.needsAllPages("signup"))
        assertEquals(listOf(hit), search.resultsFor("signup"))
    }
}
