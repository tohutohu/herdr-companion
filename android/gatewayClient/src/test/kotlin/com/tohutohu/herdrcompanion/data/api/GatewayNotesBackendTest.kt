package com.tohutohu.herdrcompanion.data.api

import com.tohutohu.herdrcompanion.data.Settings
import com.tohutohu.herdrcompanion.ui.detail.NotesUnsupportedException
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient

class GatewayNotesBackendTest {
    private fun backend(server: MockWebServer) =
        GatewayNotesBackend(GatewayApi(OkHttpClient()) { Settings(server.url("/").toString(), "secret") })

    private fun json(code: Int, body: String) = MockResponse.Builder().code(code).body(body).build()

    @Test
    fun メモを追加すると本文と期限を送り返ってきたメモを使う() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(json(201, """{"id":"n1","sessionId":"claude:a","text":"待機させておく","expiresAt":"2026-11-18T03:00:00Z"}"""))
            val note = backend(server).add("claude:a", "待機させておく", "2026-11-18T03:00:00Z")
            assertEquals("n1", note.id)
            val req = server.takeRequest()
            assertEquals("POST", req.method)
            assertEquals("/v1/sessions/claude:a/notes", req.target)
            assertEquals("""{"text":"待機させておく","expiresAt":"2026-11-18T03:00:00Z"}""", req.body?.utf8())
        }
    }

    @Test
    fun 期限なしのメモは期限を送らず書き換えは本文だけを送る() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(json(201, """{"id":"n1","text":"a"}"""))
            server.enqueue(json(200, """{"id":"n1","text":"b"}"""))
            val b = backend(server)
            b.add("claude:a", "a", null)
            assertEquals("""{"text":"a"}""", server.takeRequest().body?.utf8())
            assertEquals("b", b.update("n1", "b").text)
            val req = server.takeRequest()
            assertEquals("PATCH", req.method)
            assertEquals("/v1/notes/n1", req.target)
            assertEquals("""{"text":"b"}""", req.body?.utf8())
        }
    }

    @Test
    fun 一覧が404のGatewayはメモ非対応とし消すときの404は消えたものとして扱う() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(json(404, """{"error":"not found"}"""))
            server.enqueue(json(404, """{"error":"note not found"}"""))
            server.enqueue(json(400, """{"error":"text is required"}"""))
            val b = backend(server)
            assertFailsWith<NotesUnsupportedException> { b.list("claude:a") }
            b.delete("n1")
            val error = assertFailsWith<GatewayException> { b.add("claude:a", " ", null) }
            assertEquals("text is required", error.message)
        }
    }
}
