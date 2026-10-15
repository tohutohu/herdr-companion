package com.tohutohu.herdrcompanion.desktop

import com.tohutohu.herdrcompanion.data.api.ArchiveSessionsRequest
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import com.tohutohu.herdrcompanion.ui.sessions.SessionRef
import java.nio.file.Files
import java.util.concurrent.CopyOnWriteArrayList
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
import kotlin.test.assertTrue

class DesktopArchiveTest {
    @Test
    fun `複数選択したセッションを1回の要求でまとめてアーカイブする`(): Unit = runBlocking {
        Fixture(live = emptySet()).use { f ->
            f.waitForList()
            f.state.requestArchive(listOf(f.ref("one"), f.ref("two"), f.ref("three")))
            f.waitForArchives(1)
            assertEquals(listOf(listOf("one", "two", "three")), f.archives)
            assertTrue(f.state.archiveConfirmation.isEmpty())
        }
    }

    @Test
    fun `実行中を含む選択は確認後に全件をまとめてアーカイブする`(): Unit = runBlocking {
        Fixture(live = setOf("two")).use { f ->
            f.waitForList()
            f.state.requestArchive(listOf(f.ref("one"), f.ref("two")))
            assertEquals(listOf("one", "two"), f.state.archiveConfirmation.map { it.id })
            delay(100)
            assertTrue(f.archives.isEmpty(), "a running session must not be archived before confirmation")

            f.state.confirmArchive()
            f.waitForArchives(1)
            assertEquals(listOf(listOf("one", "two")), f.archives)
            assertTrue(f.state.archiveConfirmation.isEmpty())
        }
    }

    @Test
    fun `複数選択したセッションをすべて復元する`(): Unit = runBlocking {
        Fixture(live = emptySet()).use { f ->
            f.waitForList()
            f.state.unarchive(listOf(SessionRef("one", false, true), SessionRef("two", false, true)))
            withTimeout(5_000) {
                while (f.unarchived.size < 2) delay(10)
            }
            assertEquals(listOf("one", "two"), f.unarchived.toList())
        }
    }

    private class Fixture(live: Set<String>) : AutoCloseable {
        private val home = Files.createTempDirectory("desktop-archive-test")
        private val server = MockWebServer()
        val archives = CopyOnWriteArrayList<List<String>>()
        val unarchived = CopyOnWriteArrayList<String>()
        private val live = live
        val state: DesktopAppState

        init {
            val ids = listOf("one", "two", "three")
            fun session(id: String): String {
                val running = id in live
                val status = if (running) "running" else "offline"
                val pane = if (running) ""","paneId":"w1:$id"""" else ""
                return """{"id":"$id","provider":"claude","status":"$status","updatedAt":"2026-10-15T00:00:00Z","canSend":$running$pane}"""
            }
            server.dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse {
                    val path = request.url.encodedPath
                    if (request.method == "POST" && path == "/v1/sessions/archive") {
                        val req = GatewayApi.json.decodeFromString<ArchiveSessionsRequest>(requireNotNull(request.body).utf8())
                        archives += req.ids
                        return json("""{"sessions":[${req.ids.joinToString(",") { session(it).replace("\"status\"", "\"archived\":true,\"status\"") }}]}""")
                    }
                    if (request.method == "DELETE" && path.endsWith("/archive")) {
                        val id = path.removePrefix("/v1/sessions/").removeSuffix("/archive")
                        unarchived += id
                        return json(session(id))
                    }
                    return when (path) {
                        "/v1/sessions" -> json("""{"sessions":[${ids.joinToString(",", transform = ::session)}]}""")
                        else -> MockResponse.Builder().code(404).build()
                    }
                }
            }
            server.start()
            val source = DesktopConnectionSource(home, emptyMap(), emptyMap())
            source.saveManual(DesktopManualConnection(server.url("/").toString(), "test-token"))
            val http = OkHttpClient()
            state = DesktopAppState(source, GatewayApi(http) { source.current.settings }, http)
            state.refreshSessions()
        }

        fun ref(id: String) = SessionRef(id, id in live, false)

        suspend fun waitForList() {
            withTimeout(5_000) {
                while (state.sessions.size < 3) delay(10)
            }
        }

        suspend fun waitForArchives(count: Int) {
            withTimeout(5_000) {
                while (archives.size < count) delay(10)
            }
        }

        override fun close() {
            state.close()
            server.close()
            home.toFile().deleteRecursively()
        }

        private fun json(body: String) = MockResponse.Builder().body(body).build()
    }
}
