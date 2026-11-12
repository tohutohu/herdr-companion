package com.tohutohu.herdrcompanion.data

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class SessionLinkTest {
    @Test
    fun `セッションIDからリンクを作り元のIDに戻せる`() {
        assertEquals("hc://session/claude:0a1b-2c3d", SessionLink.url("claude:0a1b-2c3d"))
        for (id in listOf("claude:0a1b-2c3d", "codex:019a.b_c", "dsh:日本語 と空白", "a%2Fb")) {
            assertEquals(id, SessionLink.sessionId(SessionLink.url(id)))
        }
    }

    @Test
    fun `クエリとフラグメントと末尾のスラッシュを無視する`() {
        assertEquals("claude:abc", SessionLink.sessionId("hc://session/claude:abc?from=mail#top"))
        assertEquals("claude:abc", SessionLink.sessionId("HC://Session/claude%3Aabc/"))
    }

    @Test
    fun `セッションを指さないリンクは受け付けない`() {
        for (link in listOf(
            "hc://session/",
            "hc://session",
            "hc://resident/task",
            "herdr-companion://session/claude:abc",
            "https://example.com/session/claude:abc",
            "hc://session/a/b",
            "hc://session/a%2Fb",
            "hc://session/bad%zz",
            "hc://session/cut%4",
            "hc://session/line%0Abreak",
            "hc://session/%ff",
            "hc://session/" + "a".repeat(201),
        )) {
            assertNull(SessionLink.sessionId(link), link)
        }
    }
}
