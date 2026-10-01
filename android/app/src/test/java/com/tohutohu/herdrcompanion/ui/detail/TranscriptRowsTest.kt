package com.tohutohu.herdrcompanion.ui.detail

import com.tohutohu.herdrcompanion.data.Message
import com.tohutohu.herdrcompanion.data.api.BlockDto
import com.tohutohu.herdrcompanion.data.api.InteractionDto
import org.junit.Assert.assertEquals
import org.junit.Test

class TranscriptRowsTest {
    private fun text(t: String) = BlockDto(type = "text", text = t)
    private fun file(p: String) = BlockDto(type = "file", path = p)
    private fun msg(id: String, role: String, vararg blocks: BlockDto) = Message(id, role, 0, blocks.toList())

    @Test
    fun `報告の間のツール呼び出しと出力は1行にまとまる`() {
        val rows = transcriptRows(
            listOf(
                msg("u1", "user", text("認証処理直して")),
                msg("a1", "assistant", text("確認します。")),
                msg("a2", "assistant", text("Read src/auth.go"), file("src/auth.go")),
                msg("t1", "tool", text("1\tpackage auth")),
                msg("a3", "assistant", text("$ go test ./...")),
                msg("t2", "tool", text("ok")),
                msg("a4", "assistant", text("修正しました。"), file("src/auth.go")),
            ),
        )
        assertEquals(listOf("message:u1", "message:a1", "tools:a2#0", "message:a4"), rows.map { it.key })
        val group = rows[2] as ActivityRow
        assertEquals(listOf("a2", "t1", "a3", "t2"), group.parts.map { it.id })
        assertEquals(2, group.callCount)
        assertEquals("$ go test ./...", group.latest)
        assertEquals(2, group.messageIndex)
        // The file a prose message mentions stays with it.
        assertEquals(2, (rows[3] as MessageRow).message.blocks.size)
    }

    @Test
    fun `文章とツール呼び出しが同じメッセージにあれば文章だけを残す`() {
        val rows = transcriptRows(
            listOf(
                msg("a1", "assistant", text("調査します。"), text("$ ls")),
                msg("t1", "tool", text("README.md")),
                msg("a2", "assistant", text("Read a.kt"), text("続けます。")),
            ),
        )
        assertEquals(listOf("message:a1", "tools:a1#1", "message:a2#1"), rows.map { it.key })
        assertEquals(listOf("調査します。"), (rows[0] as MessageRow).message.blocks.map { it.text })
        assertEquals(listOf("$ ls", "README.md", "Read a.kt"), (rows[1] as ActivityRow).parts.flatMap { m -> m.blocks.map { it.text } })
        assertEquals(2, rows[2].messageIndex)
    }

    @Test
    fun `質問や承認はツールの出力でもまとめずに表示する`() {
        val question = BlockDto(type = "interaction", interaction = InteractionDto(id = "q1", type = "question", state = "pending"))
        val rows = transcriptRows(
            listOf(
                msg("a1", "assistant", text("$ rm -rf build")),
                msg("t1", "tool", question),
                msg("t2", "tool", text("removed")),
            ),
        )
        assertEquals(listOf("tools:a1#0", "message:t1", "tools:t2#0"), rows.map { it.key })
        assertEquals("assistant", rows[1].speaker)
    }

    @Test
    fun `Codexの呼び出しと出力をまとめたツールメッセージも呼び出しとして数える`() {
        val group = transcriptRows(
            listOf(
                msg("t1", "tool", text("$ cat src/Main.kt\nfun main() {}")),
                msg("t2", "tool", text("$ false\n(exit 1)")),
            ),
        ).single() as ActivityRow
        assertEquals(2, group.callCount)
        assertEquals("$ false", group.latest)
    }

    @Test
    fun `送信待ちから置き換わったメッセージは引き継いだキーを使う`() {
        val rows = transcriptRows(listOf(msg("u1", "user", text("続けて"))), mapOf("u1" to "pending:local"))
        assertEquals("pending:local", rows.single().key)
    }
}
