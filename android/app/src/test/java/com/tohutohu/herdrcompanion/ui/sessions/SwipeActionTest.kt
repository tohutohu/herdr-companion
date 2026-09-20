package com.tohutohu.herdrcompanion.ui.sessions

import org.junit.Assert.assertEquals
import org.junit.Test

class SwipeActionTest {
    private val offline = SessionRef("a", live = false, archived = false)
    private val running = SessionRef("b", live = true, archived = false)
    private val archived = SessionRef("c", live = false, archived = true)

    @Test
    fun `一覧のオフラインセッションはスワイプでアーカイブする`() {
        assertEquals(SwipeAction.ARCHIVE, swipeActionFor(offline))
    }

    @Test
    fun `実行中のセッションはスワイプで停止してアーカイブする`() {
        assertEquals(SwipeAction.STOP_AND_ARCHIVE, swipeActionFor(running))
    }

    @Test
    fun `アーカイブ済みのセッションはスワイプで復元する`() {
        assertEquals(SwipeAction.UNARCHIVE, swipeActionFor(archived))
    }

    @Test
    fun `停止を伴わないアーカイブだけが取り消せる`() {
        assertEquals(listOf(offline), undoableTargets(listOf(offline), stopped = false))
        assertEquals(emptyList<SessionRef>(), undoableTargets(listOf(offline, running), stopped = true))
    }

    @Test
    fun `単独の成功は完了メッセージをそのまま使う`() {
        assertEquals("Archived", BatchOutcome(total = 1, failed = 0).message("Archived", "archived"))
    }

    @Test
    fun `単独の成功でも警告があれば警告を優先する`() {
        val outcome = BatchOutcome(total = 1, failed = 0, warning = "Started without trust")
        assertEquals("Started without trust", outcome.message("Resumed in Herdr", "resumed"))
    }

    @Test
    fun `単独の失敗は理由を添える`() {
        assertEquals("Failed: boom", BatchOutcome(total = 1, failed = 1, failure = "boom").message("Archived", "archived"))
    }

    @Test
    fun `複数の結果は件数で報告する`() {
        assertEquals("3 sessions archived", BatchOutcome(total = 3, failed = 0).message("Archived", "archived"))
        assertEquals("Failed: boom", BatchOutcome(total = 2, failed = 2, failure = "boom").message("Archived", "archived"))
        assertEquals(
            "2 archived, 1 failed: boom",
            BatchOutcome(total = 3, failed = 1, failure = "boom").message("Archived", "archived"),
        )
    }
}
