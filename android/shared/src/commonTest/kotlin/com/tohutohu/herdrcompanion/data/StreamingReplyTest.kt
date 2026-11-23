package com.tohutohu.herdrcompanion.data

import com.tohutohu.herdrcompanion.data.api.BlockDto
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertSame

class StreamingReplyTest {
    private fun msg(id: String, text: String = id, queued: Boolean = false) =
        Message(id, if (id.startsWith("u")) "user" else "assistant", 0, listOf(BlockDto(type = "text", text = text)), queued)

    private fun texts(list: List<Message>) = list.map { it.id to it.blocks.single().text }

    @Test
    fun `書きかけの返答は会話の末尾に足され待ち行列のメッセージより前に置く`() {
        val reply = StreamingReply("m1", "途中まで", done = false, startedAt = 5)
        val shown = listOf(msg("u1"), msg("a1"), msg("u2", queued = true)).withStreaming(reply)
        assertEquals(
            listOf("u1" to "u1", "a1" to "a1", "streaming:m1" to "途中まで", "u2" to "u2"),
            texts(shown),
        )
        assertEquals(5, shown[2].timestamp)
    }

    @Test
    fun `Gatewayの書きかけメッセージは長いときだけ本文を差し替える`() {
        val base = listOf(msg("u1"), msg("streaming:m1", "ここまで"))
        assertEquals(
            "ここまで書いた",
            base.withStreaming(StreamingReply("m1", "ここまで書いた", done = false, startedAt = 0))[1].blocks.single().text,
        )
        assertSame(base, base.withStreaming(StreamingReply("m1", "ここ", done = false, startedAt = 0)))
        assertSame(base, base.withStreaming(null))
        assertSame(base, base.withStreaming(StreamingReply("m2", "  ", done = false, startedAt = 0)))
    }

    @Test
    fun `閉じた返答は次の取得が反映されるまで残り別の返答に移ると置き換わる`() {
        val state = StreamingReplyState()
        state.onEvent("m1", "a", done = false, now = 1)
        assertEquals(StreamingReply("m1", "ab", done = false, startedAt = 1), state.onEvent("m1", "ab", done = false, now = 2))
        assertEquals(StreamingReply("m1", "ab", done = true, startedAt = 1), state.onEvent("m1", "", done = true, now = 3))
        // A text block after a closed one (thinking first) reopens it.
        assertEquals(StreamingReply("m1", "abc", done = false, startedAt = 1), state.onEvent("m1", "abc", done = false, now = 4))
        assertEquals("abc", state.onFetched()?.text)
        assertEquals("abcd", state.onEvent("m1", "abcd", done = true, now = 5)?.text)
        assertNull(state.onFetched())
        // A closing event for a reply this client never saw adds nothing.
        assertNull(state.onEvent("m0", "late", done = true, now = 5))
        assertEquals(StreamingReply("m2", "x", done = false, startedAt = 6), state.onEvent("m2", "x", done = false, now = 6))
        assertNull(state.clear())
        assertNull(state.current)
    }
}
