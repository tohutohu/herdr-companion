package com.tohutohu.herdrmobile.data.api

import com.tohutohu.herdrmobile.data.Settings
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

class GatewayApiTest {
    private val server = MockWebServer()
    private lateinit var api: GatewayApi

    @Before
    fun setUp() {
        server.start()
        val url = server.url("/").toString().trimEnd('/')
        api = GatewayApi(OkHttpClient()) { Settings(url, "secret") }
    }

    @After
    fun tearDown() = server.close()

    @Test
    fun `メッセージ一覧をパースし未知のブロックやフィールドも受け付ける`() = runBlocking {
        server.enqueue(
            MockResponse.Builder().body(
                """
                {"session":{"id":"claude:s1","provider":"claude","providerName":"Claude Code","project":"app",
                  "status":"waiting_input","updatedAt":"2026-09-17T10:00:00Z","canSend":true,"futureField":1},
                 "messages":[
                  {"id":"m1","role":"assistant","timestamp":"2026-09-17T10:00:00Z","blocks":[
                    {"type":"text","text":"修正しました"},
                    {"type":"file","path":"src/auth.go","line":42},
                    {"type":"diff","text":"+a"},
                    {"type":"interaction","interaction":{"id":"toolu_1","type":"questions","state":"pending","supported":true,
                      "questions":[{"id":"0","type":"select","question":"どの方式？","options":[{"label":"JWT"}],"allowOther":true}]}}
                  ]}
                 ]}
                """.trimIndent(),
            ).build(),
        )
        val resp = api.messages("claude:s1", after = "m0")

        val req = server.takeRequest()
        assertEquals("Bearer secret", req.headers["Authorization"])
        assertEquals("/v1/sessions/claude:s1/messages?after=m0", req.target)

        assertEquals("waiting_input", resp.session.status)
        val blocks = resp.messages.single().blocks
        assertEquals(42, blocks[1].line)
        assertEquals("diff", blocks[2].type)
        val ia = blocks[3].interaction!!
        assertTrue(ia.isPending)
        assertEquals("JWT", ia.questions.single().options.single().label)
        assertNull(blocks[0].interaction)
    }

    @Test
    fun `エラーレスポンスのメッセージを例外に含める`() = runBlocking {
        server.enqueue(MockResponse.Builder().code(409).body("""{"error":"session is not running in herdr"}""").build())
        val e = runCatching { api.sendMessage("codex:t1", "hi", emptyList()) }.exceptionOrNull() as GatewayException
        assertEquals(409, e.code)
        assertEquals("session is not running in herdr", e.message)
        assertEquals("""{"text":"hi"}""", server.takeRequest().body?.utf8())
    }
}
