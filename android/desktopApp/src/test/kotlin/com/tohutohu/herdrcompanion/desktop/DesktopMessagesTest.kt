package com.tohutohu.herdrcompanion.desktop

import com.tohutohu.herdrcompanion.data.Message
import kotlin.test.Test
import kotlin.test.assertEquals

class DesktopMessagesTest {
    private fun msg(id: String, queued: Boolean = false) = Message(id, "user", 0, emptyList(), queued)

    @Test
    fun `キューの手前に届いた質問を差分取得で取り込む`() {
        val old = listOf(msg("a"), msg("b"), msg("queued:one", queued = true), msg("queued:two", queued = true))
        val anchor = messageAnchor(old)
        assertEquals("b", anchor)
        val incoming = listOf(msg("b"), msg("question"), msg("queued:one", queued = true), msg("queued:two", queued = true))

        val merged = mergeMessages(old, incoming, anchor)

        assertEquals(listOf("a", "b", "question", "queued:one", "queued:two"), merged.map(Message::id))
        assertEquals("question", messageAnchor(merged))
        assertEquals(listOf("a", "b", "question"), mergeMessages(merged, listOf(msg("question")), "question").map(Message::id))
    }

    @Test
    fun `キューしかない会話と空の会話は全件取得する`() {
        assertEquals(null, messageAnchor(emptyList()))
        assertEquals(null, messageAnchor(listOf(msg("queued:one", queued = true), msg("queued:two", queued = true))))
    }

    @Test
    fun `キュー属性がない古いキャッシュでも仮IDをアンカーにしない`() {
        assertEquals("a", messageAnchor(listOf(msg("a"), msg("queued:legacy"))))
    }

    @Test
    fun `差分取得ではアンカー以降を置き換える`() {
        val old = listOf(msg("a"), msg("b"), msg("c"))

        val merged = mergeMessages(old, listOf(msg("b"), msg("d")), anchor = "b")

        assertEquals(listOf("a", "b", "d"), merged.map(Message::id))
    }

    @Test
    fun `取り込まれたキュー済みメッセージは全件取得で消える`() {
        val old = listOf(msg("a"), msg("queued:t", queued = true))
        // The queued anchor is gone from the transcript, so the gateway returns everything.
        val incoming = listOf(msg("a"), msg("tool"), msg("absorbed"))

        val merged = mergeMessages(old, incoming, anchor = "queued:t")

        assertEquals(listOf("a", "tool", "absorbed"), merged.map(Message::id))
    }

    @Test
    fun `アンカーなしの取得は全件で置き換える`() {
        val merged = mergeMessages(listOf(msg("x")), listOf(msg("a")), anchor = null)

        assertEquals(listOf("a"), merged.map(Message::id))
    }
}
