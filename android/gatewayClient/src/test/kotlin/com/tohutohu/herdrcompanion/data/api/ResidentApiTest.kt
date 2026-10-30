package com.tohutohu.herdrcompanion.data.api

import com.tohutohu.herdrcompanion.data.Settings
import com.tohutohu.herdrcompanion.ui.resident.ResidentApiException
import com.tohutohu.herdrcompanion.ui.resident.ResidentDraft
import com.tohutohu.herdrcompanion.ui.resident.ResidentHomeState
import com.tohutohu.herdrcompanion.ui.resident.ResidentSupport
import com.tohutohu.herdrcompanion.ui.resident.loadResidentHome
import java.time.Duration
import java.time.Instant
import java.time.ZoneId
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertIs
import kotlin.test.assertNull
import kotlin.test.assertTrue
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient

class ResidentApiTest {
    private fun backend(server: MockWebServer) =
        ResidentGatewayBackend(GatewayApi(OkHttpClient()) { Settings(server.url("/").toString(), "secret") }, "conn")

    private fun json(code: Int, body: String) = MockResponse.Builder().code(code).body(body).build()

    @Test
    fun `常駐に対応しない旧Gatewayはタスク0件ではなく未対応と判定する`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(json(404, """{"error":"not found"}"""))
            val state = loadResidentHome(backend(server), ResidentHomeState())
            assertEquals(ResidentSupport.Unsupported, state.support)
            assertNull(state.tasks)
        }
    }

    @Test
    fun `接続できないときはタスク一覧を空にせず理由を残す`() = runBlocking {
        val server = MockWebServer()
        server.start()
        val b = backend(server)
        server.close()
        val state = loadResidentHome(b, ResidentHomeState())
        assertIs<ResidentSupport.Unknown>(state.support)
        assertNull(state.tasks)
        assertTrue(state.error!!.isNotBlank())
    }

    @Test
    fun `一覧の更新に失敗しても前回のタスクを残す`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(json(200, """{"resident":{"version":1,"defaultTimezone":"Asia/Tokyo"}}"""))
            server.enqueue(json(200, """{"tasks":[{"id":"task_1","name":"CI","provider":"claude","state":"idle","unread":2}]}"""))
            server.enqueue(json(500, """{"error":"boom","code":"internal"}"""))
            val b = backend(server)
            val first = loadResidentHome(b, ResidentHomeState())
            assertEquals(1, first.tasks?.size)
            val second = loadResidentHome(b, first)
            assertEquals(1, second.tasks?.size)
            assertEquals("boom", second.error)
        }
    }

    @Test
    fun `未信頼フォルダの拒否は理由コードで判別できる`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(json(409, """{"error":"/x was never trusted in claude","code":"trust_required"}"""))
            val e = assertFailsWith<ResidentApiException> { backend(server).create(ResidentTaskRequest(name = "x")) }
            assertTrue(e.trustRequired)
            assertEquals(409, e.status)
        }
    }

    @Test
    fun `作成要求はrequestIdを付け未指定の項目を送らない`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(json(201, """{"id":"task_1","name":"CI","provider":"claude"}"""))
            backend(server).create(ResidentTaskRequest(name = "CI", provider = "claude", requestId = "req-1", schedule = ScheduleDto("daily", time = "07:00")))
            val body = server.takeRequest().body!!.utf8()
            assertTrue(body.contains("\"requestId\":\"req-1\""), body)
            assertFalse(body.contains("model"), body)
            assertFalse(body.contains("expectedVersion"), body)
        }
    }

    @Test
    fun `実行の会話をセッションに昇格するとセッションIDを返す`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(json(200, """{"sessionId":"claude:0b6c7a43-1f5e-4a3c-9d0e-2b8f4c1a7e55"}"""))
            val id = backend(server).promoteRun("run_1")
            val req = server.takeRequest()
            assertEquals("POST", req.method)
            assertEquals("/v1/resident/runs/run_1/promote", req.target)
            assertEquals("claude:0b6c7a43-1f5e-4a3c-9d0e-2b8f4c1a7e55", id)
        }
    }

    @Test
    fun `会話を残していない実行の昇格は理由付きで失敗する`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(json(400, """{"error":"invalid request: this run kept no conversation to continue","code":"invalid"}"""))
            val e = assertFailsWith<ResidentApiException> { backend(server).promoteRun("run_1") }
            assertEquals(400, e.status)
            assertEquals("invalid", e.code)
        }
    }

    @Test
    fun `会話を続けられるのは残っているか昇格済みの実行だけ`() {
        assertTrue(canContinueAsSession("kept"))
        assertTrue(canContinueAsSession("promoted"))
        assertFalse(canContinueAsSession(null))
    }

    @Test
    fun `既定値と同じ設定も省略せずに送る`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(json(201, """{"id":"task_1","name":"CI","provider":"claude"}"""))
            backend(server).create(ResidentTaskRequest(name = "CI", schedule = ScheduleDto(ScheduleKind.MANUAL), notify = NotifyDto(), limits = LimitsDto()))
            val body = server.takeRequest().body!!.utf8()
            for (want in listOf("\"kind\":\"manual\"", "\"onFailure\":true", "\"onQuestion\":true", "\"maxRunMinutes\":30", "\"mode\":\"changes\"")) {
                assertTrue(body.contains(want), "$want missing from $body")
            }
            assertFalse(body.contains("\"model\""), body)
        }
    }

    @Test
    fun `フォームは予定と基本項目だけを送り権限や通知は既定値に任せる`() {
        val d = ResidentDraft(instruction = "CIを確認", cwd = "/w", kind = "weekly", time = "07:30", days = setOf("thu", "mon"), timezone = "Europe/London", provider = "codex")
        val s = d.schedule()
        assertEquals(listOf("mon", "thu"), s.days)
        assertEquals("07:30", s.time)
        assertNull(s.cron)
        val create = d.request(trust = false, requestId = "r1")
        assertNull(create.name) // the gateway names it after the instruction
        assertNull(create.permissions)
        assertNull(create.notify)
        assertNull(create.limits)
        assertNull(create.trust)
        // Editing can clear a model back to the default.
        assertEquals("", ResidentDraft(instruction = "x", cwd = "/w").request(trust = true, expectedVersion = 3).model)
    }

    @Test
    fun `相対時刻は日本語で前後を表す`() {
        assertEquals("3分前", ResidentGatewayBackend.relativeText(Duration.ofMinutes(-3)))
        assertEquals("2時間後", ResidentGatewayBackend.relativeText(Duration.ofMinutes(125)))
        assertEquals("まもなく", ResidentGatewayBackend.relativeText(Duration.ofSeconds(20)))
    }

    @Test
    fun `今日の時刻は時刻だけで表示する`() {
        val now = Instant.parse("2026-10-30T03:00:00Z")
        val b = ResidentGatewayBackend(GatewayApi(OkHttpClient()) { Settings() }, "c", { ZoneId.of("Asia/Tokyo") }, { now })
        assertEquals("16:00", b.formatInstant("2026-10-30T07:00:00Z"))
        assertTrue(b.formatInstant("2026-11-02T22:00:00Z").startsWith("11/3"))
    }
}
