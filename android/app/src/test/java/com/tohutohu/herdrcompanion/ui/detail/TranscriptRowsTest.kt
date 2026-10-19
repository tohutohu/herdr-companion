package com.tohutohu.herdrcompanion.ui.detail

import com.tohutohu.herdrcompanion.data.Message
import com.tohutohu.herdrcompanion.data.api.BlockDto
import com.tohutohu.herdrcompanion.data.api.InteractionDto
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class TranscriptRowsTest {
    private fun text(t: String) = BlockDto(type = "text", text = t)
    private fun file(p: String) = BlockDto(type = "file", path = p)
    private fun question(id: String) = BlockDto(type = "interaction", interaction = InteractionDto(id = id, type = "questions", state = "pending"))
    private fun msg(id: String, role: String, vararg blocks: BlockDto, at: Long = 0, queued: Boolean = false) =
        Message(id, role, at, blocks.toList(), queued)

    private val finishedTurn = listOf(
        msg("u1", "user", text("認証処理直して"), at = 1_000),
        msg("a1", "assistant", text("確認します。"), at = 2_000),
        msg("a2", "assistant", text("Read src/auth.go"), file("src/auth.go"), at = 3_000),
        msg("t1", "tool", text("1\tpackage auth"), at = 4_000),
        msg("a3", "assistant", text("$ go test ./..."), at = 5_000),
        msg("t2", "tool", text("ok"), at = 6_000),
        msg("a4", "assistant", text("修正しました。"), file("src/auth.go"), at = 87_000),
    )

    @Test
    fun `終わったターンは最後の報告だけを残し途中の報告とツール呼び出しを1行に畳む`() {
        val rows = transcriptRows(finishedTurn, lastTurn = LastTurn.DONE)
        assertEquals(listOf("message:u1", "work:u1#0", "message:a4"), rows.map { it.key })
        val work = rows[1] as WorkRow
        assertEquals(listOf("message:a1", "tools:a2#0"), work.rows.map { it.key })
        assertEquals(listOf("a2", "t1", "a3", "t2"), (work.rows[1] as ActivityRow).parts.map { it.id })
        assertEquals(2, work.callCount)
        assertEquals(86_000L, work.durationMs)
        assertFalse(work.working)
        assertEquals(1, work.messageIndex)
        // The file the answer mentions stays with it, and the turn ends under it.
        val answer = rows[2] as MessageRow
        assertEquals(2, answer.message.blocks.size)
        assertEquals(TurnEnd(failed = false, at = 87_000), answer.end)
        assertNull(work.end)
    }

    @Test
    fun `作業中のターンは最新の途中報告とその後のツール呼び出しを表示する`() {
        val rows = transcriptRows(
            listOf(
                msg("u1", "user", text("認証処理直して")),
                msg("a1", "assistant", text("確認します。")),
                msg("a2", "assistant", text("Read src/auth.go")),
                msg("a3", "assistant", text("原因が分かったので直します。")),
                msg("a4", "assistant", text("$ go test ./...")),
            ),
            lastTurn = LastTurn.WORKING,
        )
        assertEquals(listOf("message:u1", "work:u1#0", "message:a3", "tools:a4#0"), rows.map { it.key })
        val work = rows[1] as WorkRow
        assertTrue(work.working)
        assertNull(work.durationMs)
        assertEquals(1, work.callCount)
        assertEquals("$ go test ./...", (rows[3] as ActivityRow).latest)
        assertTrue(rows.all { it.end == null })
    }

    @Test
    fun `作業中でまだ報告がなければツール呼び出しの行だけを表示する`() {
        val rows = transcriptRows(
            listOf(msg("u1", "user", text("調べて")), msg("a1", "assistant", text("$ ls"))),
            lastTurn = LastTurn.WORKING,
        )
        assertEquals(listOf("message:u1", "tools:a1#0"), rows.map { it.key })
    }

    @Test
    fun `次の指示が続くターンはセッションが作業中でも終わったものとして畳む`() {
        val rows = transcriptRows(
            finishedTurn + listOf(msg("u2", "user", text("ありがとう"), at = 90_000), msg("a5", "assistant", text("$ git diff"))),
            lastTurn = LastTurn.WORKING,
        )
        assertEquals(listOf("message:u1", "work:u1#0", "message:a4", "message:u2", "tools:a5#0"), rows.map { it.key })
        assertEquals(TurnEnd(failed = false, at = 87_000), rows[2].end)
        assertNull(rows[4].end)
    }

    @Test
    fun `報告のない終わったターンは作業をすべて畳み失敗も示す`() {
        val rows = transcriptRows(
            listOf(
                msg("u1", "user", text("ビルドして"), at = 1_000),
                msg("a1", "assistant", text("$ ./gradlew build"), at = 1_000),
                msg("e1", "system", text("Turn failed: overloaded"), at = 31_000),
            ),
            lastTurn = LastTurn.FAILED,
        )
        assertEquals(listOf("message:u1", "work:u1#0"), rows.map { it.key })
        val work = rows[1] as WorkRow
        assertEquals(30_000L, work.durationMs)
        assertEquals(TurnEnd(failed = true, at = 31_000), work.end)
    }

    @Test
    fun `すぐ答えたターンは畳むものがなくても所要時間の行を出す`() {
        val rows = transcriptRows(
            listOf(msg("u1", "user", text("今何時?"), at = 1_000), msg("a1", "assistant", text("19時です。"), at = 4_000)),
            lastTurn = LastTurn.DONE,
        )
        assertEquals(listOf("message:u1", "work:u1#0", "message:a1"), rows.map { it.key })
        val work = rows[1] as WorkRow
        assertTrue(work.rows.isEmpty())
        assertEquals(3_000L, work.durationMs)
    }

    @Test
    fun `時刻が分からないターンは所要時間を出さない`() {
        val rows = transcriptRows(
            listOf(msg("u1", "user", text("今何時?")), msg("a1", "assistant", text("19時です。"))),
            lastTurn = LastTurn.DONE,
        )
        assertEquals(listOf("message:u1", "message:a1"), rows.map { it.key })
    }

    @Test
    fun `回答待ちの質問とその後は畳まない`() {
        val rows = transcriptRows(
            listOf(
                msg("u1", "user", text("片付けて")),
                msg("a1", "assistant", text("消してよいか確認します。")),
                msg("a2", "assistant", text("$ ls build")),
                msg("t1", "tool", question("q1")),
                // Codex keeps working while an async question waits.
                msg("a3", "assistant", text("待つ間に他を進めます。")),
            ),
            lastTurn = LastTurn.WORKING,
        )
        assertEquals(listOf("message:u1", "work:u1#0", "message:t1", "message:a3"), rows.map { it.key })
        assertEquals(listOf("message:a1", "tools:a2#0"), (rows[1] as WorkRow).rows.map { it.key })
    }

    @Test
    fun `まだ取り込まれていない指示はターンを区切らず作業の後に表示する`() {
        val rows = transcriptRows(
            listOf(
                msg("u1", "user", text("直して")),
                msg("a1", "assistant", text("直しています。")),
                msg("q", "user", text("ついでにREADMEも"), queued = true),
                msg("a2", "assistant", text("$ go test ./...")),
            ),
            lastTurn = LastTurn.WORKING,
        )
        assertEquals(listOf("message:u1", "message:a1", "tools:a2#0", "message:q"), rows.map { it.key })
    }

    @Test
    fun `文章とツール呼び出しが同じメッセージにあれば文章だけを残す`() {
        val rows = transcriptRows(
            listOf(
                msg("a1", "assistant", text("調査します。"), text("$ ls")),
                msg("t1", "tool", text("README.md")),
                msg("a2", "assistant", text("Read a.kt"), text("続けます。")),
            ),
            lastTurn = LastTurn.DONE,
        )
        assertEquals(listOf("work:a1#0", "message:a2#1"), rows.map { it.key })
        val work = (rows[0] as WorkRow).rows
        assertEquals(listOf("調査します。"), (work[0] as MessageRow).message.blocks.map { it.text })
        assertEquals(listOf("$ ls", "README.md", "Read a.kt"), (work[1] as ActivityRow).parts.flatMap { m -> m.blocks.map { it.text } })
        assertEquals(2, rows[1].messageIndex)
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
    fun `畳まれた報告もその作業の行が表示しているものとして探せる`() {
        val rows = transcriptRows(finishedTurn, lastTurn = LastTurn.DONE)
        assertEquals(1, rows.indexOfFirst { it.shows("a1") })
        assertEquals(1, rows.indexOfFirst { it.shows("t2") })
        assertEquals(2, rows.indexOfFirst { it.shows("a4") })
    }

    @Test
    fun `送信待ちから置き換わったメッセージは引き継いだキーを使う`() {
        val rows = transcriptRows(listOf(msg("u1", "user", text("続けて"))), mapOf("u1" to "pending:local"))
        assertEquals("pending:local", rows.single().key)
    }

    @Test
    fun `IDが重複したり空だったりするメッセージにも別々のキーを付ける`() {
        val rows = transcriptRows(
            listOf(
                msg("", "system", text("Unsupported event")),
                msg("", "system", text("Unsupported event")),
                msg("a1", "assistant", text("報告")),
                msg("a1", "assistant", text("報告")),
            ),
        )
        assertEquals(listOf("work:#0", "message:a1"), rows.map { it.key })
        assertEquals(listOf("message:", "message:@1", "message:a1"), (rows[0] as WorkRow).rows.map { it.key })
    }

    @Test
    fun `所要時間は秒と分と時間で表す`() {
        assertEquals("1s", durationLabel(400))
        assertEquals("45s", durationLabel(45_000))
        assertEquals("1m 26s", durationLabel(86_000))
        assertEquals("1m 03s", durationLabel(63_000))
        assertEquals("1h 05m", durationLabel(3_900_000))
    }
}
