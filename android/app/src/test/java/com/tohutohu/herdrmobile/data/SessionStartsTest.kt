package com.tohutohu.herdrmobile.data

import com.tohutohu.herdrmobile.data.api.StartSessionRequest
import com.tohutohu.herdrmobile.data.api.StartSessionResponse
import kotlinx.coroutines.*
import org.junit.Assert.*
import org.junit.Test

class SessionStartsTest {
    private val request = StartSessionRequest("claude", "/workspace/project", "hello", false)

    @Test fun `起動応答前に表示し成功後は一覧と照合する`() = runBlocking {
        val response = CompletableDeferred<StartSessionResponse>()
        val starts = SessionStarts(this, { response.await() }, { _, _ -> error("unused") }, {})
        val id = starts.enqueue(request)
        assertTrue(starts.entries.value.single().busy)
        assertEquals(request, starts.entries.value.single().request)
        yield()
        response.complete(StartSessionResponse(sessionId = "real", paneId = "pane"))
        yield()
        assertEquals(id, starts.entries.value.single().id)
        assertEquals("real", starts.entries.value.single().sessionId)
        starts.reconcile(setOf("real"))
        starts.reconcile(emptySet())
        assertTrue(starts.entries.value.single().listed)
    }

    @Test fun `セッションIDが遅れて判明してもペインで照合する`() = runBlocking {
        val starts = SessionStarts(this, { StartSessionResponse(paneId = "pane") }, { _, _ -> error("unused") }, {})
        starts.enqueue(request)
        yield()
        starts.reconcile(setOf("real"), mapOf("pane" to "real"))
        assertEquals("real", starts.entries.value.single().sessionId)
        assertTrue(starts.entries.value.single().listed)
    }

    @Test fun `信頼確認の回答は二重送信しない`() = runBlocking {
        var calls = 0
        val starts = SessionStarts(this,
            { StartSessionResponse(paneId = "pane", trustRequired = true) },
            { _, _ -> calls++; StartSessionResponse(sessionId = "real", paneId = "pane") }, {})
        val id = starts.enqueue(request)
        yield()
        assertEquals("pane", starts.entries.value.single().trustPane)
        starts.answerTrust(id, true)
        starts.answerTrust(id, true)
        yield()
        assertEquals(1, calls)
        assertEquals("real", starts.entries.value.single().sessionId)
    }

    @Test fun `一覧取得失敗で起動成功を取り消さない`() = runBlocking {
        val starts = SessionStarts(this,
            { StartSessionResponse(sessionId = "real", paneId = "pane") },
            { _, _ -> error("unused") }, { error("offline") })
        starts.enqueue(request)
        yield()
        assertEquals("real", starts.entries.value.single().sessionId)
        assertNull(starts.entries.value.single().notice)
    }

    @Test fun `起動失敗を保持して自動再送しない`() = runBlocking {
        var calls = 0
        val starts = SessionStarts(this, { calls++; error("timeout") }, { _, _ -> error("unused") }, {})
        val id = starts.enqueue(request)
        yield()
        assertFalse(starts.entries.value.single().busy)
        assertTrue(starts.entries.value.single().notice!!.contains("timeout"))
        assertEquals(1, calls)
        starts.dismiss(id)
        assertTrue(starts.entries.value.isEmpty())
    }

    @Test fun `端末待ちでは一覧のID判明だけで初期プロンプトを飛ばさない`() = runBlocking {
        var calls = 0
        val response = CompletableDeferred<StartSessionResponse>()
        val starts = SessionStarts(this,
            { StartSessionResponse(paneId = "pane", warning = "Open terminal") },
            { _, _ -> error("unused") }, {},
            { calls++; response.await() })
        val id = starts.enqueue(request)
        yield()
        starts.reconcile(setOf("real"), mapOf("pane" to "real"))
        assertNull(starts.entries.value.single().sessionId)
        assertEquals("pane", starts.entries.value.single().paneId)
        starts.continueLaunch(id)
        starts.continueLaunch(id)
        yield()
        response.complete(StartSessionResponse(sessionId = "real", paneId = "pane"))
        yield()
        assertEquals(1, calls)
        assertEquals("real", starts.entries.value.single().sessionId)
    }

    @Test fun `信頼を拒否したペインは端末への導線に残さない`() = runBlocking {
        val starts = SessionStarts(this,
            { StartSessionResponse(paneId = "pane", trustRequired = true) },
            { _, _ -> StartSessionResponse(paneId = "pane") }, {})
        val id = starts.enqueue(request)
        yield()
        starts.answerTrust(id, false)
        yield()
        assertNull(starts.entries.value.single().paneId)
    }
}
