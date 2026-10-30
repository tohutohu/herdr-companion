package com.tohutohu.herdrcompanion.data

import com.tohutohu.herdrcompanion.data.api.StartSessionRequest
import com.tohutohu.herdrcompanion.data.api.StartSessionResponse
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
        response.complete(StartSessionResponse(sessionId = "real"))
        yield()
        assertEquals(id, starts.entries.value.single().id)
        assertEquals("real", starts.entries.value.single().sessionId)
        starts.reconcile(setOf("real"))
        starts.reconcile(emptySet())
        assertTrue(starts.entries.value.single().listed)
    }

    @Test fun `添付をアップロードしてからそのIDを付けて起動する`() = runBlocking {
        var sent: StartSessionRequest? = null
        val starts = SessionStarts(this, { sent = it; StartSessionResponse(sessionId = "real") }, { _, _ -> error("unused") }, {})
        starts.enqueue(request) { listOf("up1", "up2") }
        yield()
        assertEquals(request.copy(uploads = listOf("up1", "up2")), sent)
        assertEquals("real", starts.entries.value.single().sessionId)
    }

    @Test fun `添付のアップロードに失敗したら起動しない`() = runBlocking {
        var calls = 0
        val starts = SessionStarts(this, { calls++; StartSessionResponse(sessionId = "real") }, { _, _ -> error("unused") }, {})
        starts.enqueue(request) { error("too large") }
        yield()
        val entry = starts.entries.value.single()
        assertEquals(0, calls)
        assertFalse(entry.busy)
        assertTrue(entry.notice!!.contains("too large"))
        assertTrue(entry.notice!!.contains("not started"))
    }

    @Test fun `信頼確認の回答は起動IDへ一度だけ送る`() = runBlocking {
        val answered = mutableListOf<Pair<String, Boolean>>()
        val starts = SessionStarts(this,
            { StartSessionResponse(launchId = "launch-1", trustRequired = true) },
            { launch, trust -> answered += launch to trust; StartSessionResponse(sessionId = "claude:s1") }, {})
        val id = starts.enqueue(request)
        yield()
        assertEquals("launch-1", starts.entries.value.single().trustLaunch)
        starts.answerTrust(id, true)
        starts.answerTrust(id, true)
        yield()
        assertEquals(listOf("launch-1" to true), answered)
        val started = starts.entries.value.single()
        assertEquals("claude:s1", started.sessionId)
        assertNull(started.trustLaunch)
    }

    @Test fun `一覧取得失敗で起動成功を取り消さない`() = runBlocking {
        val starts = SessionStarts(this,
            { StartSessionResponse(sessionId = "real") },
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

    @Test fun `警告付きの起動も一覧と照合する`() = runBlocking {
        val starts = SessionStarts(this,
            { StartSessionResponse(sessionId = "codex:t1", warning = "slow start") },
            { _, _ -> error("unused") }, {})
        starts.enqueue(request)
        yield()
        val entry = starts.entries.value.single()
        assertEquals("slow start", entry.notice)
        starts.reconcile(setOf("codex:t1"))
        assertTrue(starts.entries.value.single().listed)
    }

    @Test fun `信頼を拒否すると起動IDを残さず取り消しを伝える`() = runBlocking {
        val starts = SessionStarts(this,
            { StartSessionResponse(launchId = "launch-2", trustRequired = true) },
            { _, _ -> StartSessionResponse(launchId = "launch-2") }, {})
        val id = starts.enqueue(request)
        yield()
        starts.answerTrust(id, false)
        yield()
        val entry = starts.entries.value.single()
        assertNull(entry.trustLaunch)
        assertNull(entry.sessionId)
        assertEquals("Session start cancelled.", entry.notice)
    }

    @Test fun `再開が途中で止まったら一覧から辿れるよう残す`() = runBlocking {
        val starts = SessionStarts(this, { error("unused") }, { _, _ -> error("unused") }, {})
        starts.trackResume(request, StartSessionResponse(launchId = "launch-3", trustRequired = true, warning = "trust"))
        val entry = starts.entries.value.single()
        assertFalse(entry.busy)
        assertEquals("launch-3", entry.trustLaunch)
        assertEquals("trust", entry.notice)
    }
}
