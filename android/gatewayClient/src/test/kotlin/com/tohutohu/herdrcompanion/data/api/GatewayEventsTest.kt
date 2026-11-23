package com.tohutohu.herdrcompanion.data.api

import com.tohutohu.herdrcompanion.data.Settings
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.take
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient

class GatewayEventsTest {
    private fun sse(vararg events: String) = MockResponse.Builder()
        .code(200)
        .addHeader("Content-Type", "text/event-stream")
        .body("retry: 3000\n\n" + events.joinToString(""))
        .build()

    @Test
    fun `イベントを種類ごとに読み取り知らない種類は無視する`() {
        assertEquals(GatewayEvent.Ready(reset = true), parseGatewayEvent("ready", """{"epoch":"e","reset":true}"""))
        assertEquals(
            GatewayEvent.SessionChanged("claude:a", "running", removed = false),
            parseGatewayEvent("session", """{"sessionId":"claude:a","status":"running"}"""),
        )
        assertEquals(GatewayEvent.SessionsChanged, parseGatewayEvent("sessions", "{}"))
        assertEquals(GatewayEvent.ResidentChanged, parseGatewayEvent("resident", "{}"))
        assertEquals(
            GatewayEvent.Streaming("codex:t", "i1", "こんにちは", done = false),
            parseGatewayEvent("streaming", """{"sessionId":"codex:t","messageId":"i1","text":"こんにちは"}"""),
        )
        assertNull(parseGatewayEvent("future", "{}"))
        assertNull(parseGatewayEvent("session", "not json"))
    }

    @Test
    fun `接続すると届いたイベントを流し切れたら最後のIDで再接続する`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(
                sse(
                    "event: ready\ndata: {\"epoch\":\"e\",\"reset\":false}\n\n",
                    ": ping\n\n",
                    "id: e-7\nevent: session\ndata: {\"sessionId\":\"claude:a\",\"status\":\"running\"}\n\n",
                    "event: streaming\ndata: {\"sessionId\":\"claude:a\",\"messageId\":\"m\",\"text\":\"x\"}\n\n",
                ),
            )
            server.enqueue(sse("event: ready\ndata: {\"epoch\":\"e\",\"reset\":false}\n\n", "id: e-8\nevent: sessions\ndata: {}\n\n"))
            val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
            try {
                val api = GatewayApi(OkHttpClient()) { Settings(server.url("/").toString(), "secret") }
                val stream = GatewayEventStream(api, OkHttpClient(), scope, retryDelays = listOf(10))
                val got = withTimeout(10_000) { stream.events.take(5).toList() }
                assertEquals(
                    listOf(
                        GatewayEvent.Ready(false),
                        GatewayEvent.SessionChanged("claude:a", "running", false),
                        GatewayEvent.Streaming("claude:a", "m", "x", false),
                        GatewayEvent.Ready(false),
                        GatewayEvent.SessionsChanged,
                    ),
                    got,
                )
                val first = server.takeRequest()
                assertEquals("/v1/events", first.url.encodedPath)
                assertEquals("Bearer secret", first.headers["Authorization"])
                assertNull(first.headers["Last-Event-ID"])
                assertEquals("e-7", server.takeRequest().headers["Last-Event-ID"])
            } finally {
                scope.cancel()
            }
        }
    }

    @Test
    fun `イベントのないGatewayには接続済みにならず問い合わせを控える`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(MockResponse.Builder().code(404).body("""{"error":"not found"}""").build())
            val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
            try {
                val api = GatewayApi(OkHttpClient()) { Settings(server.url("/").toString(), "secret") }
                val stream = GatewayEventStream(api, OkHttpClient(), scope, retryDelays = listOf(10))
                val job = launch { stream.events.collect {} }
                withTimeout(5_000) { while (server.requestCount == 0) delay(10) }
                delay(300)
                assertFalse(stream.connected.value)
                assertEquals(1, server.requestCount)
                job.cancelAndJoin()
            } finally {
                scope.cancel()
            }
        }
    }

    @Test
    fun `関係するイベントが届くと待たずに取り直す`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(
                MockResponse.Builder()
                    .code(200)
                    .addHeader("Content-Type", "text/event-stream")
                    .body("event: ready\ndata: {}\n\n")
                    .bodyDelay(0, TimeUnit.SECONDS)
                    .build(),
            )
            val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
            try {
                val api = GatewayApi(OkHttpClient()) { Settings(server.url("/").toString(), "secret") }
                val stream = GatewayEventStream(api, OkHttpClient(), scope, retryDelays = listOf(60_000))
                val refreshed = Channel<Long>(Channel.UNLIMITED)
                val started = System.currentTimeMillis()
                val poll = launch {
                    stream.pollWithEvents(period = 60_000, connectedPeriod = 60_000, relevant = { it.affectsSessionList() }) {
                        refreshed.send(System.currentTimeMillis() - started)
                    }
                }
                withTimeout(10_000) {
                    refreshed.receive() // the first, at once
                    refreshed.receive() // the ready event
                }
                assertTrue(System.currentTimeMillis() - started < 10_000)
                poll.cancelAndJoin()
            } finally {
                scope.cancel()
            }
        }
    }

    @Test
    fun `会話の取り直しは自分のセッションの変化と完了した返答だけで起こす`() {
        assertTrue(GatewayEvent.SessionChanged("claude:a", null, false).affectsSession("claude:a"))
        assertFalse(GatewayEvent.SessionChanged("claude:b", null, false).affectsSession("claude:a"))
        assertTrue(GatewayEvent.Streaming("claude:a", "m", "", done = true).affectsSession("claude:a"))
        assertFalse(GatewayEvent.Streaming("claude:a", "m", "x", done = false).affectsSession("claude:a"))
        assertFalse(GatewayEvent.SessionsChanged.affectsSession("claude:a"))
        assertTrue(GatewayEvent.Ready(false).affectsSession("claude:a"))
        assertTrue(GatewayEvent.SessionsChanged.affectsSessionList())
        assertFalse(GatewayEvent.ResidentChanged.affectsSessionList())
    }
}
