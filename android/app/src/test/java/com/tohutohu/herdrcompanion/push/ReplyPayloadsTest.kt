package com.tohutohu.herdrcompanion.push

import java.nio.file.Files
import org.junit.After
import org.junit.Assert.*
import org.junit.Test

class ReplyPayloadsTest {
    private val directory = Files.createTempDirectory("herdr-replies-").toFile()
    private val store = ReplyPayloadStore(directory)
    private fun payload(reply: String = "続けて") = ReplyPayload(connectionId = "connection", sessionId = "claude:1",
        status = "completed", title = "Title", body = "Body", reply = reply, createdAt = 100)

    @After fun cleanup() { directory.deleteRecursively() }

    @Test fun `大きな日本語と絵文字の返信をプロセス再開後も完全に読み出せる`() {
        val original = store.create(payload("日本語🙂\n".repeat(40_000)))
        assertEquals(original, ReplyPayloadStore(directory).load(original.id))
        assertEquals(1, store.pendingIds("connection").size)
    }

    @Test fun `同時刻の連続返信も入力順を保持し別IDになる`() {
        val first = store.create(payload("先の返信"))
        val second = store.create(payload("後の返信"))
        assertNotEquals(first.id, second.id)
        assertEquals(listOf(first.id, second.id), ReplyPayloadStore(directory).pendingIds("connection"))
    }

    @Test fun `送信開始と終端状態は原文を維持して保存する`() {
        val original = store.create(payload())
        val attempted = original.copy(attempted = true, safeToRetry = true)
        store.save(attempted)
        assertEquals(attempted, ReplyPayloadStore(directory).load(original.id))
        store.save(attempted.copy(terminal = true))
        assertTrue(store.pendingIds("connection").isEmpty())
    }

    @Test fun `正常終了後は本文ファイルを削除する`() {
        val original = store.create(payload())
        store.delete(original.id)
        assertNull(store.load(original.id))
    }

    @Test fun `別接続と古い終端データを消しオフライン待機中の本文は残す`() {
        val pending = store.create(payload())
        val finished = store.create(payload()).copy(terminal = true)
        store.save(finished)
        store.create(payload().copy(connectionId = "old"))
        store.cleanup("connection", now = 31L * 24 * 60 * 60 * 1_000)
        assertEquals(listOf(pending.id), store.pendingIds("connection"))
        assertNull(store.load(finished.id))
        assertEquals(1, directory.listFiles().orEmpty().size)
    }

    @Test fun `不正IDからprivateディレクトリの外へアクセスしない`() {
        assertThrows(IllegalArgumentException::class.java) { store.load("../secret") }
        assertThrows(IllegalArgumentException::class.java) { store.save(payload().copy(id = "../secret")) }
    }

    @Test fun `送信開始前は旧Gatewayにも一回送れる`() {
        assertEquals(ReplyDecision.SEND, resumedReplyDecision(payload(), gatewaySupportsReceipts = false))
    }

    @Test fun `旧Gatewayへの送信開始後は曖昧な失敗とプロセス再開で再送しない`() {
        val attempted = payload().copy(attempted = true)
        assertEquals(ReplyDecision.UNCONFIRMED, resumedReplyDecision(attempted, false))
        assertEquals(ReplyDecision.UNCONFIRMED, failedReplyDecision(attempted, null, 0))
    }

    @Test fun `永続receipt対応Gatewayだけ同じIDで再試行できる`() {
        val attempted = payload().copy(attempted = true, safeToRetry = true)
        assertEquals(ReplyDecision.SEND, resumedReplyDecision(attempted, true))
        assertEquals(ReplyDecision.RETRY, failedReplyDecision(attempted, null, 0))
        assertEquals(ReplyDecision.UNCONFIRMED, resumedReplyDecision(attempted, false))
        assertEquals(ReplyDecision.UNCONFIRMED, failedReplyDecision(attempted, 409, 0))
        assertEquals(ReplyDecision.UNCONFIRMED, failedReplyDecision(attempted, null, 3))
    }

    @Test fun `通信開始前の一時障害は再試行し恒久エラーは後続を止めず終える`() {
        assertEquals(ReplyDecision.RETRY, failedReplyDecision(payload(), null, 0))
        assertEquals(ReplyDecision.FINISH, failedReplyDecision(payload(), 401, 0))
        assertEquals(ReplyDecision.FINISH, failedReplyDecision(payload(), null, 3))
        assertEquals(ReplyDecision.FINISH, resumedReplyDecision(payload().copy(terminal = true), true))
    }

    @Test fun `通知履歴は短い表示に限定し絵文字のサロゲートを分断しない`() {
        val reply = "a".repeat(1_999) + "🙂" + "z".repeat(10_000)
        assertEquals("a".repeat(1_999) + "…", replyHistoryPreview(reply))
        assertEquals("🙂短文", replyHistoryPreview("🙂短文"))
    }
}
