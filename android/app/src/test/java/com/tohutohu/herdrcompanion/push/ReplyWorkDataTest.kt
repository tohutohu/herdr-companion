package com.tohutohu.herdrcompanion.push

import android.app.Application
import androidx.work.workDataOf
import java.nio.file.Files
import java.util.UUID
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], application = Application::class)
class ReplyWorkDataTest {
    @Test fun `保存本文が残るWork成功は配信成功と見なさず復旧し取消は尊重する`() {
        // A temporary payload read error may finish Work without attempting a
        // send. The saved body must be retried on restart instead of deleted.
        assertTrue(shouldResumeSavedReply(listOf(androidx.work.WorkInfo.State.SUCCEEDED)))
        assertTrue(shouldResumeSavedReply(emptyList()))
        org.junit.Assert.assertFalse(shouldResumeSavedReply(listOf(androidx.work.WorkInfo.State.RUNNING)))
        org.junit.Assert.assertFalse(shouldResumeSavedReply(listOf(androidx.work.WorkInfo.State.SUCCEEDED, androidx.work.WorkInfo.State.CANCELLED)))
    }
    @Test fun `10KBを超える返信本文はWorkManagerへIDだけ渡す`() {
        val directory = Files.createTempDirectory("reply-work-").toFile()
        try {
            val store = ReplyPayloadStore(directory)
            val payload = store.create(ReplyPayload(connectionId = "connection", sessionId = "claude:s1",
                status = "completed", title = "Title", body = "Body", reply = "日本語🙂".repeat(40_000)))
            val data = ReplyWorker.workData(payload.id)
            assertEquals(mapOf("reply_payload_id" to payload.id), data.keyValueMap)
            assertTrue(data.toByteArray().size < 512)
            assertEquals(payload.reply, ReplyPayloadStore(directory).load(payload.id)?.reply)
        } finally {
            directory.deleteRecursively()
        }
    }

    @Test fun `旧APKが既にenqueueした返信は本文を保持し送信結果不明として移行する`() {
        val id = UUID.randomUUID().toString()
        val original = workDataOf("session_id" to "claude:s1", "reply" to "旧本文🙂", "connection_id" to "connection")
        val payload = legacyReplyPayload(original, id)!!
        assertEquals(id, payload.id)
        assertEquals("旧本文🙂", payload.reply)
        assertEquals(ReplyDecision.UNCONFIRMED, resumedReplyDecision(payload, true))
    }
}
