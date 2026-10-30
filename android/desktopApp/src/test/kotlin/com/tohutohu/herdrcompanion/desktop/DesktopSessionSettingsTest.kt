package com.tohutohu.herdrcompanion.desktop

import com.tohutohu.herdrcompanion.data.api.GatewayApi
import java.nio.file.Files
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.OkHttpClient
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull

class DesktopSessionSettingsTest {
    @Test
    fun `直接実行のセッションのモデル変更は保留中の値を表示し拒否理由をそのまま示す`(): Unit = runBlocking {
        Fixture().use { f ->
            f.waitFor { f.state.detailFor(SESSION_ID)?.session != null }
            f.state.loadSettings(SESSION_ID)
            f.waitFor { f.state.detailFor(SESSION_ID)?.settings?.catalog != null }
            assertEquals(listOf("opus", "sonnet"), f.state.detailFor(SESSION_ID)!!.settings.catalog!!.models.map { it.id })

            f.state.setModel(SESSION_ID, "gpt-9")
            f.waitFor { f.state.detailFor(SESSION_ID)?.settings?.changeError != null }
            assertEquals(REFUSAL, f.state.detailFor(SESSION_ID)!!.settings.changeError)
            assertNull(f.state.detailFor(SESSION_ID)!!.session!!.pendingModel)

            f.state.setModel(SESSION_ID, "sonnet")
            f.waitFor { f.state.detailFor(SESSION_ID)?.session?.pendingModel == "sonnet" }
            val settings = f.state.detailFor(SESSION_ID)!!.settings
            assertNull(settings.changeError)
            assertEquals("next_request", f.state.detailFor(SESSION_ID)!!.session!!.settingsApply)
            assertEquals(listOf("""{"model":"gpt-9"}""", """{"model":"sonnet"}"""), f.modelBodies.toList())

            // The detail poll rebuilds the state; the loaded catalog stays.
            val polls = f.messagePolls.get()
            f.waitFor(timeout = 10_000) { f.messagePolls.get() > polls && f.state.detailFor(SESSION_ID)?.session?.pendingModel == "sonnet" }
            assertNotNull(f.state.detailFor(SESSION_ID)!!.settings.catalog)
        }
    }

    @Test
    fun `実行中の直接実行セッションを中断する`(): Unit = runBlocking {
        Fixture().use { f ->
            f.waitFor { f.state.detailFor(SESSION_ID)?.session != null }
            f.state.interrupt(SESSION_ID)
            f.waitFor { f.interrupts.get() == 1 && f.state.detailFor(SESSION_ID)?.actionBusy == false }
        }
    }

    private class Fixture : AutoCloseable {
        private val home = Files.createTempDirectory("desktop-settings-test")
        private val server = MockWebServer()
        val modelBodies = CopyOnWriteArrayList<String>()
        val messagePolls = AtomicInteger()
        val interrupts = AtomicInteger()
        @Volatile private var pendingModel: String? = null
        val state: DesktopAppState

        private fun session(): String {
            val pending = pendingModel?.let { ""","pendingModel":"$it","settingsApply":"next_request"""" }.orEmpty()
            return """{"id":"$SESSION_ID","provider":"claude","status":"running","updatedAt":"2026-10-30T00:00:00Z",""" +
                """"canSend":true,"backend":"direct","model":"opus"$pending}"""
        }

        init {
            server.dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse {
                    val path = request.url.encodedPath
                    if (request.method == "POST" && path == "/v1/sessions/$SESSION_ID/model") {
                        val body = requireNotNull(request.body).utf8()
                        modelBodies += body
                        if (body.contains("gpt-9")) {
                            return MockResponse.Builder().code(400).body("""{"error":"$REFUSAL"}""").build()
                        }
                        pendingModel = "sonnet"
                        return json(session())
                    }
                    if (request.method == "POST" && path == "/v1/sessions/$SESSION_ID/interrupt") {
                        interrupts.incrementAndGet()
                        return MockResponse.Builder().code(202).body("""{"ok":true}""").build()
                    }
                    return when (path) {
                        "/v1/sessions" -> json("""{"sessions":[${session()}]}""")
                        "/v1/sessions/$SESSION_ID" -> json(session())
                        "/v1/sessions/$SESSION_ID/messages" -> {
                            messagePolls.incrementAndGet()
                            json("""{"session":${session()},"messages":[]}""")
                        }
                        "/v1/sessions/$SESSION_ID/seen" -> json("{}")
                        "/v1/models" -> json(
                            """{"models":[{"id":"opus","name":"Opus","default":true},{"id":"sonnet","name":"Sonnet"}],""" +
                                """"efforts":[{"id":"high","name":"High"}]}""",
                        )
                        else -> MockResponse.Builder().code(404).build()
                    }
                }
            }
            server.start()
            val source = DesktopConnectionSource(home, emptyMap(), emptyMap())
            source.saveManual(DesktopManualConnection(server.url("/").toString(), "test-token"))
            val http = OkHttpClient()
            state = DesktopAppState(source, GatewayApi(http) { source.current.settings }, http)
            state.openSession(SESSION_ID)
        }

        suspend fun waitFor(timeout: Long = 5_000, condition: () -> Boolean) {
            withTimeout(timeout) {
                while (!condition()) delay(10)
            }
        }

        override fun close() {
            state.close()
            server.close()
            home.toFile().deleteRecursively()
        }

        private fun json(body: String) = MockResponse.Builder().body(body).build()
    }

    private companion object {
        const val SESSION_ID = "claude:direct"
        const val REFUSAL = "invalid setting: model gpt-9 is not available; choose one of: opus, sonnet"
    }
}
