package com.tohutohu.herdrcompanion.data.api

import com.tohutohu.herdrcompanion.data.Settings
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
import java.io.ByteArrayOutputStream

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
    fun `メディアURLは特殊文字を保持し認証情報をURLに含めない`() {
        val path = "/workspace/動画 #1 & test.mp4"
        val request = api.mediaRequest("claude:s/1", path)
        assertEquals(path, request.url.queryParameter("path"))
        assertEquals("1", request.url.queryParameter("download"))
        assertEquals("claude:s/1", request.url.pathSegments[2])
        assertEquals("Bearer secret", request.header("Authorization"))
        assertTrue(!request.url.toString().contains("secret"))
    }

    @Test
    fun `画像URLを認証付きでストリームしレスポンス情報を返す`() = runBlocking {
        server.enqueue(
            MockResponse.Builder()
                .addHeader("Content-Type", "image/png; charset=binary")
                .body("png")
                .build(),
        )
        val out = ByteArrayOutputStream()
        var headers: DownloadMetadata? = null
        var progress = 0L
        val metadata = api.downloadUrl(
            "/v1/sessions/claude:s1/messages/m1/images/0",
            out,
            onHeaders = { headers = it },
            onProgress = { progress = it },
        )

        val req = server.takeRequest()
        assertEquals("/v1/sessions/claude:s1/messages/m1/images/0", req.target)
        assertEquals("Bearer secret", req.headers["Authorization"])
        assertEquals("image/png", metadata.mimeType)
        assertEquals(3L, metadata.contentLength)
        assertEquals(metadata, headers)
        assertEquals(3L, progress)
        assertEquals("png", out.toString())
    }

    @Test
    fun `フォルダ判定は指示とパスを認証付きで送り起動しない`() = runBlocking {
        server.enqueue(MockResponse.Builder().body("""{"verdict":"mismatch","historyCount":3}""").build())
        val result = api.checkDirectory("/workspace/app", "通知を直して")
        assertEquals("mismatch", result.verdict)
        assertEquals(3, result.historyCount)
        val req = server.takeRequest()
        assertEquals("POST", req.method)
        assertEquals("/v1/directories/check", req.target)
        assertEquals("Bearer secret", req.headers["Authorization"])
        val sent = GatewayApi.json.decodeFromString<DirectoryCheckRequest>(req.body!!.utf8())
        assertEquals("/workspace/app", sent.cwd)
        assertEquals("通知を直して", sent.prompt)
        assertEquals(1, server.requestCount)
    }

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
    fun `エージェントが入力待ちで送れないときは対処が分かるメッセージにする`() = runBlocking {
        server.enqueue(
            MockResponse.Builder().code(409)
                .body("""{"error":"send message: herdr: agent_blocked: agent wZ:p1 is blocked and requires interactive input"}""")
                .build(),
        )
        val e = runCatching { api.sendMessage("claude:s1", "hi", emptyList()) }.exceptionOrNull() as GatewayException
        assertEquals(409, e.code)
        assertEquals("The agent is waiting for an answer to a prompt. Answer it, then send again.", e.message)
    }

    @Test
    fun `セッションのモード変更は専用APIを呼ぶ`() = runBlocking {
        server.enqueue(MockResponse.Builder().code(202).body("""{"ok":true}""").build())
        api.cycleMode("claude:s1")
        val req = server.takeRequest()
        assertEquals("POST", req.method)
        assertEquals("/v1/sessions/claude:s1/mode", req.target)
        assertEquals("Bearer secret", req.headers["Authorization"])
    }

    @Test
    fun `アップロードは本文の種別と元のファイル名を送る`() = runBlocking {
        server.enqueue(MockResponse.Builder().code(201).body("""{"id":"abc","name":"売上_レポート.csv"}""").build())
        assertEquals("abc", api.upload("col1\n".toByteArray(), "text/csv", "売上 レポート.csv"))
        val req = server.takeRequest()
        assertEquals("/v1/uploads", req.target)
        assertEquals("text/csv", req.headers["Content-Type"])
        assertEquals(
            "attachment; filename*=UTF-8''%E5%A3%B2%E4%B8%8A%20%E3%83%AC%E3%83%9D%E3%83%BC%E3%83%88.csv",
            req.headers["Content-Disposition"],
        )
    }

    @Test
    fun `モデル一覧を取得し起動リクエストにモデルとエフォートとモードを含める`() = runBlocking {
        server.enqueue(
            MockResponse.Builder().body(
                """{"models":[{"id":"gpt-6-astra","name":"GPT-6 Astra","default":true,""" +
                    """"efforts":[{"id":"xhigh","name":"Extra high"}]},""" +
                    """{"id":"gpt-6-mini","name":"Mini","description":"Fast"}],""" +
                    """"efforts":[{"id":"medium","name":"Medium","default":true}],""" +
                    """"modes":[{"id":"default","name":"Default","default":true},{"id":"plan","name":"Plan"}]}""",
            ).build(),
        )
        val catalog = api.models("codex")
        assertEquals("/v1/models?provider=codex", server.takeRequest().target)
        assertEquals(listOf("gpt-6-astra", "gpt-6-mini"), catalog.models.map { it.id })
        assertTrue(catalog.models[0].default)
        assertEquals(listOf("xhigh"), catalog.models[0].efforts.map { it.id })
        assertEquals("Fast", catalog.models[1].description)
        assertEquals(listOf("medium"), catalog.efforts.map { it.id })
        assertEquals(listOf("default", "plan"), catalog.modes.map { it.id })
        assertTrue(catalog.modes[0].default)

        server.enqueue(MockResponse.Builder().code(201).body("""{"sessionId":"codex:t1","paneId":"w1:p1"}""").build())
        api.startSession(StartSessionRequest("codex", "/w/app", "", true, "gpt-6-mini", "xhigh", "plan"))
        val body = server.takeRequest().body!!.utf8()
        assertTrue(body.contains("\"model\":\"gpt-6-mini\""))
        assertTrue(body.contains("\"effort\":\"xhigh\""))
        assertTrue(body.contains("\"mode\":\"plan\""))

        server.enqueue(MockResponse.Builder().code(201).body("""{"paneId":"w1:p2"}""").build())
        api.startSession(StartSessionRequest("codex", "/w/app", "", true))
        val plain = server.takeRequest().body!!.utf8()
        assertTrue(!plain.contains("model") && !plain.contains("effort") && !plain.contains("mode"))

        server.enqueue(MockResponse.Builder().code(201).body("""{"paneId":"w1:p3","trustRequired":true}""").build())
        assertTrue(api.startSession(StartSessionRequest("codex", "/w/app", "", false)).trustRequired)
        server.takeRequest()
        server.enqueue(MockResponse.Builder().code(200).body("""{"sessionId":"codex:t3","paneId":"w1:p3"}""").build())
        assertEquals("codex:t3", api.answerTrust("w1:p3", trust = true).sessionId)
        val answer = server.takeRequest()
        assertEquals("/v1/launches/w1:p3/trust", answer.target)
        assertEquals("""{"trust":true}""", answer.body!!.utf8())
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

    @Test
    fun `メッセージの条件付き取得は変更がなければnullを返しETagを同じ要求にだけ送る`() = runBlocking {
        val body = """{"session":{"id":"claude:s1","provider":"claude","status":"running","updatedAt":"2026-09-17T10:00:00Z"},"messages":[]}"""
        server.enqueue(MockResponse.Builder().addHeader("ETag", "\"v1\"").body(body).build())
        server.enqueue(MockResponse.Builder().code(304).addHeader("ETag", "\"v1\"").build())
        server.enqueue(MockResponse.Builder().addHeader("ETag", "\"v2\"").body(body).build())

        assertEquals("running", api.messagesIfChanged("claude:s1", after = "m1")!!.session.status)
        assertNull(server.takeRequest().headers["If-None-Match"])

        assertNull(api.messagesIfChanged("claude:s1", after = "m1"))
        assertEquals("\"v1\"", server.takeRequest().headers["If-None-Match"])

        // 別の anchor への要求には前回の ETag を送らない。
        assertEquals("running", api.messagesIfChanged("claude:s1", after = "m2")!!.session.status)
        assertNull(server.takeRequest().headers["If-None-Match"])
    }

    @Test
    fun `複数セッションのアーカイブは一度のリクエストで送る`() = runBlocking {
        val first = """{"id":"claude:s1","provider":"claude","status":"offline","updatedAt":"2026-09-17T10:00:00Z","archived":true}"""
        val second = first.replace("claude:s1", "codex:s2")
        server.enqueue(MockResponse.Builder().body("""{"sessions":[$first,$second]}""").build())

        val archived = api.archive(listOf("claude:s1", "codex:s2"))

        assertEquals(listOf("claude:s1", "codex:s2"), archived.map { it.id })
        val req = server.takeRequest()
        assertEquals("POST", req.method)
        assertEquals("/v1/sessions/archive", req.target)
        assertEquals("""{"ids":["claude:s1","codex:s2"]}""", req.body!!.utf8())
        assertEquals("Bearer secret", req.headers["Authorization"])
    }
}
