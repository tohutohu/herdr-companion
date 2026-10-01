package com.tohutohu.herdrcompanion.ui.sessions

import com.tohutohu.herdrcompanion.data.api.SessionDto
import com.tohutohu.herdrcompanion.data.api.SessionsResponse
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ArchivedSessionsPagerTest {
    private val all = (0 until 5).map { SessionDto(id = "claude:s$it", provider = "claude", status = "offline", updatedAt = "") }

    /** Pages over [ids] like the gateway, recording each request. */
    private class FakeGateway(var ids: List<SessionDto>) {
        val requests = mutableListOf<Pair<Int, Int>>()
        var fail = false

        fun fetch(offset: Int, limit: Int): SessionsResponse {
            requests += offset to limit
            if (fail) error("offline")
            val end = if (limit == 0) ids.size else minOf(ids.size, offset + limit)
            return SessionsResponse(ids.subList(minOf(offset, ids.size), end), nextOffset = end.takeIf { it < ids.size })
        }
    }

    private fun ids(pager: ArchivedSessionsPager) = pager.sessions.orEmpty().map { it.id.removePrefix("claude:") }

    @Test
    fun `最初のページだけ読み込み続きを順に追加する`() = runBlocking {
        val gateway = FakeGateway(all)
        val pager = ArchivedSessionsPager(pageSize = 2, fetch = gateway::fetch)

        pager.load(keepLoaded = false)
        assertEquals(listOf("s0", "s1"), ids(pager))
        assertTrue(pager.hasMore)

        pager.loadMore()
        pager.loadMore()
        assertEquals(listOf("s0", "s1", "s2", "s3", "s4"), ids(pager))
        assertFalse(pager.hasMore)
        pager.loadMore()
        assertEquals(listOf(0 to 2, 2 to 2, 4 to 2), gateway.requests)
    }

    @Test
    fun `変更後の再読み込みは読み込み済みの範囲を保つ`() = runBlocking {
        val gateway = FakeGateway(all)
        val pager = ArchivedSessionsPager(pageSize = 2, fetch = gateway::fetch)
        pager.load(keepLoaded = false)
        pager.loadMore()

        // s1 のアーカイブを解除した
        gateway.ids = all - all[1]
        pager.load(keepLoaded = true)
        assertEquals(listOf("s0", "s2", "s3", "s4"), ids(pager))
        assertEquals(0 to 4, gateway.requests.last())

        // すべて読み込んだ後は残りを全部読み直す
        pager.load(keepLoaded = true)
        assertEquals(0 to 0, gateway.requests.last())
        // 引っ張って更新したときは最初のページに戻る
        pager.load(keepLoaded = false)
        assertEquals(listOf("s0", "s2"), ids(pager))
    }

    @Test
    fun `ページがずれても同じセッションを重複させない`() = runBlocking {
        val gateway = FakeGateway(all)
        val pager = ArchivedSessionsPager(pageSize = 2, fetch = gateway::fetch)
        pager.load(keepLoaded = false)

        // 別の端末で新しくアーカイブされ、先頭に1件増えた
        gateway.ids = listOf(SessionDto(id = "claude:new", provider = "claude", status = "offline", updatedAt = "")) + all
        pager.loadMore()
        assertEquals(listOf("s0", "s1", "s2"), ids(pager))
    }

    @Test
    fun `続きの読み込みに失敗したら再読み込みまで止める`() = runBlocking {
        val gateway = FakeGateway(all)
        val pager = ArchivedSessionsPager(pageSize = 2, fetch = gateway::fetch)
        pager.load(keepLoaded = false)

        gateway.fail = true
        assertTrue(runCatching { pager.loadMore() }.isFailure)
        assertFalse(pager.hasMore)
        pager.loadMore()
        assertEquals(2, gateway.requests.size)

        gateway.fail = false
        pager.load(keepLoaded = false)
        assertTrue(pager.hasMore)
    }
}
