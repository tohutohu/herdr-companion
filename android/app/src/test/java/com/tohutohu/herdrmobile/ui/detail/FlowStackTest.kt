package com.tohutohu.herdrmobile.ui.detail

import com.tohutohu.herdrmobile.data.Message
import com.tohutohu.herdrmobile.data.api.BlockDto
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class FlowStackTest {
    private fun msg(id: String, role: String, vararg texts: String) =
        Message(id, role, 0, texts.map { BlockDto(type = "text", text = it) })

    private val messages = listOf(
        msg("u1", "user", "最初の指示"),
        msg("a1", "assistant", "調査します。", "$ ls"),
        msg("t1", "tool", "README.md"),
        msg("a2", "assistant", "完了しました。"),
        msg("u2", "user", "effort を\n選べるようにして"),
        msg("a3", "assistant", "Next: claude プロバイダ。", "実装\n$ go test ./..."),
        msg("t2", "tool", "ok"),
        msg("a4", "assistant", "Read gateway/main.go"),
        msg("a5", "assistant", "Next: launcher と API。"),
        msg("t3", "tool", "True"),
    )

    @Test
    fun `最下部では最新の指示とそのターンの報告だけが積まれる`() {
        val stack = flowStack(messages, firstVisible = 9)
        assertEquals(FlowEntry("u2", "effort を 選べるようにして"), stack.instruction)
        assertEquals(
            listOf(FlowEntry("a3", "Next: claude プロバイダ。"), FlowEntry("a5", "Next: launcher と API。")),
            stack.reports,
        )
    }

    @Test
    fun `画面内に見えている報告は積まない`() {
        val stack = flowStack(messages, firstVisible = 6)
        assertEquals("u2", stack.instruction?.messageId)
        assertEquals(listOf("a3"), stack.reports.map { it.messageId })
    }

    @Test
    fun `指示が画面の先頭に見えている間は何も積まない`() {
        assertTrue(flowStack(messages, firstVisible = 4).isEmpty)
        assertTrue(flowStack(messages, firstVisible = 0).isEmpty)
    }

    @Test
    fun `遡ると表示中のターンの指示と報告に切り替わる`() {
        val stack = flowStack(messages, firstVisible = 3)
        assertEquals("u1", stack.instruction?.messageId)
        assertEquals(listOf(FlowEntry("a1", "調査します。")), stack.reports)
    }

    @Test
    fun `指示より前の報告は指示なしで積む`() {
        val stack = flowStack(messages.drop(1), firstVisible = 2)
        assertEquals(null, stack.instruction)
        assertEquals(listOf("a1"), stack.reports.map { it.messageId })
    }

    @Test
    fun `画像だけの指示は代替テキストで積む`() {
        val image = Message("u", "user", 0, listOf(BlockDto(type = "image", url = "/x")))
        val stack = flowStack(listOf(image, msg("a", "assistant", "見ました")), firstVisible = 2)
        assertEquals(FlowEntry("u", "(attachment)"), stack.instruction)
    }

    @Test
    fun `メッセージがなければ空`() {
        assertTrue(flowStack(emptyList(), firstVisible = 0).isEmpty)
    }
}
