package com.tohutohu.herdrcompanion.data.api

import com.tohutohu.herdrcompanion.data.Settings
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.withContext
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient
import okhttp3.Call
import okhttp3.EventListener
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
        server.enqueue(MockResponse.Builder().code(409).body("""{"error":"session is not running"}""").build())
        val e = runCatching { api.sendMessage("codex:t1", "hi", emptyList()) }.exceptionOrNull() as GatewayException
        assertEquals(409, e.code)
        assertEquals("session is not running", e.message)
        assertEquals("""{"text":"hi"}""", server.takeRequest().body?.utf8())
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
    fun `既読化は既読APIを呼ぶ`() = runBlocking {
        server.enqueue(MockResponse.Builder().body("""{"ok":true}""").build())
        api.markSeen("claude:s1")
        val req = server.takeRequest()
        assertEquals("POST", req.method)
        assertEquals("/v1/sessions/claude:s1/seen", req.target)
    }

    @Test
    fun `アップロードは本文の種別と元のファイル名を送る`() = runBlocking {
        server.enqueue(MockResponse.Builder().code(201).body("""{"id":"abc","name":"売上_レポート.csv"}""").build())
        assertEquals("abc", api.upload("text/csv", "売上 レポート.csv") { "col1\n".byteInputStream() })
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

        server.enqueue(MockResponse.Builder().code(201).body("""{"sessionId":"codex:t1"}""").build())
        api.startSession(StartSessionRequest("codex", "/w/app", "", true, "gpt-6-mini", "xhigh", "plan"))
        val body = server.takeRequest().body!!.utf8()
        assertTrue(body.contains("\"model\":\"gpt-6-mini\""))
        assertTrue(body.contains("\"effort\":\"xhigh\""))
        assertTrue(body.contains("\"mode\":\"plan\""))

        server.enqueue(MockResponse.Builder().code(201).body("""{"sessionId":"codex:t2"}""").build())
        api.startSession(StartSessionRequest("codex", "/w/app", "", true))
        val plain = server.takeRequest().body!!.utf8()
        assertTrue(!plain.contains("model") && !plain.contains("effort") && !plain.contains("mode"))
        assertTrue(!plain.contains("worktree"))

        server.enqueue(MockResponse.Builder().code(201).body("""{"sessionId":"codex:t4"}""").build())
        api.startSession(StartSessionRequest("codex", "/w/app", "", true, worktree = true))
        assertTrue(server.takeRequest().body!!.utf8().contains("\"worktree\":true"))

        server.enqueue(MockResponse.Builder().code(201).body("""{"launchId":"launch-3","trustRequired":true}""").build())
        assertTrue(api.startSession(StartSessionRequest("codex", "/w/app", "", false)).trustRequired)
        server.takeRequest()
        server.enqueue(MockResponse.Builder().code(200).body("""{"sessionId":"codex:t3"}""").build())
        assertEquals("codex:t3", api.answerTrust("launch-3", trust = true).sessionId)
        val answer = server.takeRequest()
        assertEquals("/v1/launches/launch-3/trust", answer.target)
        assertEquals("""{"trust":true}""", answer.body!!.utf8())
    }

    @Test
    fun `アーカイブ一覧の取得とアーカイブ解除と再開のリクエストを送る`() = runBlocking {
        val session = """{"id":"claude:s1","provider":"claude","status":"offline","updatedAt":"2026-09-17T10:00:00Z","archived":true}"""
        server.enqueue(MockResponse.Builder().body("""{"sessions":[$session],"nextOffset":30}""").build())
        val page = api.archivedSessions(offset = 0, limit = 30)
        assertEquals("/v1/sessions?archived=true&offset=0&limit=30", server.takeRequest().target)
        assertEquals(30, page.nextOffset)
        val archived = page.sessions
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

        server.enqueue(MockResponse.Builder().code(201).body("""{"sessionId":"claude:s1","warning":"dialog"}""").build())
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

        val snapshot = api.messagesIfChanged("claude:s1", after = "m1")!!
        assertEquals("running", snapshot.response.session.status)
        snapshot.acknowledge()
        assertNull(server.takeRequest().headers["If-None-Match"])

        assertNull(api.messagesIfChanged("claude:s1", after = "m1"))
        assertEquals("\"v1\"", server.takeRequest().headers["If-None-Match"])

        // 別の anchor への要求には前回の ETag を送らない。
        assertEquals("running", api.messagesIfChanged("claude:s1", after = "m2")!!.response.session.status)
        assertNull(server.takeRequest().headers["If-None-Match"])
    }

    @Test
    fun `ローカル反映に失敗した応答のETagは次の取得を省略しない`() = runBlocking {
        val body = """{"session":{"id":"claude:s1","provider":"claude","status":"running","updatedAt":"2026-09-17T10:00:00Z"},"messages":[]}"""
        repeat(3) { index ->
            server.enqueue(MockResponse.Builder().addHeader("ETag", "\"v${index + 1}\"").body(body).build())
        }
        api.messagesIfChanged("claude:s1", after = "m1")!!.acknowledge()
        assertNull(server.takeRequest().headers["If-None-Match"])

        // A failed Room write or desktop mapping leaves the newer response unacknowledged.
        api.messagesIfChanged("claude:s1", after = "m1")!!
        assertEquals("\"v1\"", server.takeRequest().headers["If-None-Match"])

        api.messagesIfChanged("claude:s1", after = "m1")!!
        assertEquals("\"v1\"", server.takeRequest().headers["If-None-Match"])
    }

    @Test
    fun `応答受信中にキャンセルされても未反映のETagを記憶しない`() = runBlocking {
        val body = """{"session":{"id":"claude:s1","provider":"claude","status":"running","updatedAt":"2026-09-17T10:00:00Z"},"messages":[]}"""
        repeat(2) { server.enqueue(MockResponse.Builder().addHeader("ETag", "\"v1\"").body(body).build()) }
        val receiving = Job()
        val http = OkHttpClient.Builder().eventListener(object : EventListener() {
            override fun responseBodyEnd(call: Call, byteCount: Long) {
                // The blocking read finishes, but withContext cannot deliver its result.
                receiving.cancel()
            }
        }).build()
        val cancellingApi = GatewayApi(http) { Settings(server.url("/").toString(), "secret") }

        val failure = runCatching {
            withContext(receiving) { cancellingApi.messagesIfChanged("claude:s1", after = "m1") }
        }.exceptionOrNull()
        assertTrue(failure is CancellationException)
        assertNull(server.takeRequest().headers["If-None-Match"])

        cancellingApi.messagesIfChanged("claude:s1", after = "m1")!!
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

    @Test
    fun `アーカイブ時にworktreeを残した理由とフォルダがリポジトリかどうかを受け取る`() = runBlocking {
        val kept = """{"id":"claude:s1","provider":"claude","status":"offline","updatedAt":"2026-09-17T10:00:00Z","archived":true,""" +
            """"warning":"Worktree kept because it has uncommitted changes: /w/a"}"""
        server.enqueue(MockResponse.Builder().body(kept).build())
        assertEquals("Worktree kept because it has uncommitted changes: /w/a", api.archive("claude:s1").warning)
        server.takeRequest()

        server.enqueue(MockResponse.Builder().body("""{"sessions":[$kept]}""").build())
        assertEquals(listOf("Worktree kept because it has uncommitted changes: /w/a"), api.archive(listOf("claude:s1")).map { it.warning })
        server.takeRequest()

        server.enqueue(MockResponse.Builder().body("""{"path":"/w/app","entries":[],"git":true}""").build())
        assertTrue(api.directories("/w/app").git)
        server.takeRequest()
        // 古いGatewayは git を返さないので worktree は選べない
        server.enqueue(MockResponse.Builder().body("""{"path":"/w/app","entries":[]}""").build())
        assertTrue(!api.directories("/w/app").git)
    }
    @Test
    fun `セッションのモデルとエフォートを変更し保留中の値を受け取る`() = runBlocking {
        val session = """{"id":"claude:s1","provider":"claude","status":"running","updatedAt":"2026-10-30T10:00:00Z",""" +
            """"model":"opus","pendingModel":"sonnet","settingsApply":"next_request"}"""
        server.enqueue(MockResponse.Builder().body(session).build())
        val changed = api.setModel("claude:s1", "sonnet")
        assertEquals("sonnet", changed.pendingModel)
        assertEquals("next_request", changed.settingsApply)
        server.takeRequest().let {
            assertEquals("POST", it.method)
            assertEquals("/v1/sessions/claude:s1/model", it.target)
            assertEquals("""{"model":"sonnet"}""", it.body!!.utf8())
        }

        server.enqueue(MockResponse.Builder().body(session.replace("\"pendingModel\":\"sonnet\"", "\"pendingEffort\":\"high\"")).build())
        assertEquals("high", api.setEffort("claude:s1", "high").pendingEffort)
        server.takeRequest().let {
            assertEquals("/v1/sessions/claude:s1/effort", it.target)
            assertEquals("""{"effort":"high"}""", it.body!!.utf8())
        }

        server.enqueue(MockResponse.Builder().body(session).build())
        api.setEffort("claude:s1", "max", model = "opus")
        assertEquals("""{"model":"opus","effort":"max"}""", server.takeRequest().body!!.utf8())
    }

    @Test
    fun `提供されていないモデルへの変更はゲートウェイの理由をそのまま伝える`() = runBlocking {
        val reason = "invalid setting: model gpt-9 is not available; choose one of: opus, sonnet"
        server.enqueue(MockResponse.Builder().code(400).body("""{"error":"$reason"}""").build())
        val e = runCatching { api.setModel("claude:s1", "gpt-9") }.exceptionOrNull() as GatewayException
        assertEquals(400, e.code)
        assertEquals(reason, e.message)
    }

    @Test
    fun `中断とモードの指定はそれぞれのAPIを呼びモードの巡回は本文なしで送る`() = runBlocking {
        server.enqueue(MockResponse.Builder().code(202).body("""{"ok":true}""").build())
        api.interrupt("codex:t1")
        server.takeRequest().let {
            assertEquals("POST", it.method)
            assertEquals("/v1/sessions/codex:t1/interrupt", it.target)
        }

        server.enqueue(MockResponse.Builder().code(202).body("""{"ok":true}""").build())
        api.setMode("codex:t1", "plan")
        server.takeRequest().let {
            assertEquals("/v1/sessions/codex:t1/mode", it.target)
            assertEquals("""{"mode":"plan"}""", it.body!!.utf8())
        }

        server.enqueue(MockResponse.Builder().code(202).body("""{"ok":true}""").build())
        api.cycleMode("codex:t1")
        assertEquals(0, server.takeRequest().body?.size ?: 0)
    }

    @Test
    fun `信頼確認の要る起動は起動IDを受け取り回答をそこへ送る`() = runBlocking {
        server.enqueue(MockResponse.Builder().code(201).body("""{"launchId":"launch-1","trustRequired":true}""").build())
        val started = api.startSession(StartSessionRequest("claude", "/w/new", "", true))
        assertTrue(started.trustRequired)
        assertEquals("launch-1", started.launchId)
        assertNull(started.sessionId)
        server.takeRequest()

        server.enqueue(MockResponse.Builder().body("""{"sessionId":"claude:s9"}""").build())
        val answered = api.answerTrust(started.launchId!!, trust = true)
        assertEquals("claude:s9", answered.sessionId)
        assertEquals("/v1/launches/launch-1/trust", server.takeRequest().target)
    }
}
