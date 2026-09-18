package com.tohutohu.herdrmobile.data

import com.tohutohu.herdrmobile.data.api.BlockDto
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class OutboxTest {
    private fun msg(id: String, role: String, text: String?, queued: Boolean = false) =
        Message(id, role, 0, listOfNotNull(text?.let { BlockDto(type = "text", text = it) }), queued)

    private fun pending(
        id: String,
        text: String,
        known: Set<String> = setOf("u1", "a1"),
        state: SendState = SendState.ACCEPTED,
        acceptedAt: Long? = 0,
        session: String = "claude:s",
    ) = PendingMessage(id, session, text, emptyList(), state, known = known, acceptedAt = acceptedAt)

    private val before = listOf(msg("u1", "user", "続けて"), msg("a1", "assistant", "はい"))

    @Test
    fun `送信前からある同じ文面のメッセージとは結び付けない`() {
        val p = pending("p1", "続けて")
        assertTrue(matchPending(listOf(p), before).isEmpty())

        val after = before + msg("u2", "user", "続けて")
        assertEquals("u2", matchPending(listOf(p), after)["p1"]?.id)
    }

    @Test
    fun `空白の違いと末尾に足された添付パスは無視して見つける`() {
        val p = pending("p1", "README を\n  直して")
        val after = before + msg("u2", "user", "README を 直して\n/tmp/uploads/x/report.csv")
        assertEquals("u2", matchPending(listOf(p), after)["p1"]?.id)
    }

    @Test
    fun `続けて送った同じ文面はそれぞれ別のメッセージに結び付く`() {
        val ps = listOf(pending("p1", "ok"), pending("p2", "ok"))
        val oneArrived = before + msg("u2", "user", "ok")
        assertEquals(setOf("p1"), matchPending(ps, oneArrived).keys)

        val both = oneArrived + msg("u3", "user", "ok")
        val found = matchPending(ps, both)
        assertEquals("u2", found["p1"]?.id)
        assertEquals("u3", found["p2"]?.id)
    }

    @Test
    fun `添付だけのメッセージは次に現れたユーザーメッセージに結び付く`() {
        val p = pending("p1", "")
        val after = before + msg("a2", "assistant", "見ます") + msg("u2", "user", "[Image #1]")
        assertEquals("u2", matchPending(listOf(p), after)["p1"]?.id)
    }

    @Test
    fun `キュー待ちの間は保留に残り、会話に入ったら外れる`() {
        val all = listOf(pending("p1", "テストも直して"))
        val queued = before + msg("queued:1", "user", "テストも直して", queued = true)
        assertEquals(listOf("p1"), stillPending(all, "claude:s", queued, now = 1).map { it.localId })

        val taken = before + msg("u2", "user", "テストも直して")
        assertTrue(stillPending(all, "claude:s", taken, now = 1).isEmpty())
    }

    @Test
    fun `失敗扱いでもエージェントに届いていれば外れる`() {
        val all = listOf(pending("p1", "続けて", state = SendState.FAILED, acceptedAt = null))
        val after = before + msg("u2", "user", "続けて")
        assertTrue(stillPending(all, "claude:s", after, now = 1).isEmpty())
    }

    @Test
    fun `受付から長く現れないものは手放し、送信中や失敗は残す`() {
        val hour = 60L * 60 * 1000
        val all = listOf(
            pending("accepted", "a", acceptedAt = 0),
            pending("sending", "b", state = SendState.SENDING, acceptedAt = null),
            pending("failed", "c", state = SendState.FAILED, acceptedAt = null),
        )
        assertEquals(3, stillPending(all, "claude:s", before, now = 10L * 60 * 1000).size)
        assertEquals(listOf("sending", "failed"), stillPending(all, "claude:s", before, now = hour).map { it.localId })
    }

    @Test
    fun `他のセッションの保留には触れない`() {
        val all = listOf(pending("p1", "続けて", session = "codex:t"))
        val after = before + msg("u2", "user", "続けて")
        assertEquals(all, stillPending(all, "claude:s", after, now = Long.MAX_VALUE))
    }
}
