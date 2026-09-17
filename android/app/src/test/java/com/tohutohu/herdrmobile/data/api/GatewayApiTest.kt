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

    @Test
    fun `モデル一覧を取得し起動リクエストにモデルとエフォートを含める`() = runBlocking {
        server.enqueue(
            MockResponse.Builder().body(
                """{"models":[{"id":"gpt-6-astra","name":"GPT-6 Astra","default":true,""" +
                    """"efforts":[{"id":"xhigh","name":"Extra high"}]},""" +
                    """{"id":"gpt-6-mini","name":"Mini","description":"Fast"}],""" +
                    """"efforts":[{"id":"medium","name":"Medium","default":true}]}""",
            ).build(),
        )
        val catalog = api.models("codex")
        assertEquals("/v1/models?provider=codex", server.takeRequest().target)
        assertEquals(listOf("gpt-6-astra", "gpt-6-mini"), catalog.models.map { it.id })
        assertTrue(catalog.models[0].default)
        assertEquals(listOf("xhigh"), catalog.models[0].efforts.map { it.id })
        assertEquals("Fast", catalog.models[1].description)
        assertEquals(listOf("medium"), catalog.efforts.map { it.id })

        server.enqueue(MockResponse.Builder().code(201).body("""{"sessionId":"codex:t1","paneId":"w1:p1"}""").build())
        api.startSession(StartSessionRequest("codex", "/w/app", "", true, "gpt-6-mini", "xhigh"))
        val body = server.takeRequest().body!!.utf8()
        assertTrue(body.contains("\"model\":\"gpt-6-mini\""))
        assertTrue(body.contains("\"effort\":\"xhigh\""))

        server.enqueue(MockResponse.Builder().code(201).body("""{"paneId":"w1:p2"}""").build())
        api.startSession(StartSessionRequest("codex", "/w/app", "", true))
        val plain = server.takeRequest().body!!.utf8()
        assertTrue(!plain.contains("model") && !plain.contains("effort"))
    }

    @Test
    fun `アーカイブ一覧の取得とアーカイブ解除と再開のリクエストを送る`() = runBlocking {
        val session = """{"id":"claude:s1","provider":"claude","status":"offline","updatedAt":"2026-09-17T10:00:00Z","archived":true}"""
        server.enqueue(MockResponse.Builder().body("""{"sessions":[$session]}""").build())
        val archived = api.archivedSessions()
        assertEquals("/v1/sessions?archived=true", server.takeRequest().target)
        assertTrue(archived.single().archived)
        assertTrue(!archived.single().isLive)

        server.enqueue(MockResponse.Builder().body(session).build())
        assertTrue(api.archive("claude:s1").archived)
        server.takeRequest().let {
            assertEquals("POST", it.method)
            assertEquals("/v1/sessions/claude:s1/archive", it.target)
        }

        server.enqueue(MockResponse.Builder().body(session.replace("\"archived\":true", "\"archived\":false")).build())
        assertTrue(!api.unarchive("claude:s1").archived)
        assertEquals("DELETE", server.takeRequest().method)

        server.enqueue(MockResponse.Builder().code(201).body("""{"sessionId":"claude:s1","paneId":"w2:p1","warning":"dialog"}""").build())
        assertEquals("dialog", api.resume("claude:s1", trust = true).warning)
        server.takeRequest().let {
            assertEquals("/v1/sessions/claude:s1/resume", it.target)
            assertEquals("""{"trust":true}""", it.body!!.utf8())
        }
    }
}
