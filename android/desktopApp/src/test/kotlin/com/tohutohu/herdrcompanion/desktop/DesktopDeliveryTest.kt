package com.tohutohu.herdrcompanion.desktop

import com.tohutohu.herdrcompanion.data.SendState
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import com.tohutohu.herdrcompanion.data.api.SendMessageRequest
import com.tohutohu.herdrcompanion.ui.detail.PendingMessageUiState
import java.nio.file.Files
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import mockwebserver3.SocketEffect
import okhttp3.OkHttpClient
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class DesktopDeliveryTest {
    @Test
    fun `応答を失っても同じ本文と要求IDと添付IDで再送し二重Retryは送らない`(): Unit = runBlocking {
        Fixture(receipts = true, loseFirstResponse = true, gatedPost = 2).use { f ->
            f.waitForDetail()
            val file = f.home.resolve("attachment.txt")
            Files.writeString(file, "initial attachment")
            f.state.addAttachments(SESSION_ID, listOf(file))
            f.state.send(SESSION_ID, "unchanged prompt")
            val failed = f.waitForPending(SendState.FAILED)
            assertTrue(failed.canRetry)
            assertEquals(1, f.uploadCount.get())
            // A later edit to the original file must not change an already
            // attempted request's uploaded content or payload.
            Files.writeString(file, "modified after the first delivery attempt")

            f.state.retry(SESSION_ID, failed.localId)
            f.state.retry(SESSION_ID, failed.localId)
            assertTrue(f.gateArrived.await(5, TimeUnit.SECONDS))
            assertEquals(2, f.posts.size)
            assertEquals(1, f.uploadCount.get())
            f.releaseGate.countDown()
            f.waitForPending(SendState.ACCEPTED)

            assertEquals(2, f.posts.size)
            assertEquals(f.posts[0], f.posts[1], "retry payload must be byte-for-byte identical")
            val request = GatewayApi.json.decodeFromString<SendMessageRequest>(f.posts[0])
            assertEquals(failed.localId, request.requestId)
            assertEquals("unchanged prompt", request.text)
            assertEquals(listOf("upload-1"), request.uploads)
            assertEquals(listOf("initial attachment"), f.uploadedBodies.toList())
        }
    }

    @Test
    fun `旧Gatewayの応答を失った送信は履歴確認を促して再実行しない`(): Unit = runBlocking {
        Fixture(receipts = false, loseFirstResponse = true).use { f ->
            f.waitForDetail()
            f.state.send(SESSION_ID, "possibly delivered")
            val failed = f.waitForPending(SendState.FAILED)
            assertFalse(failed.canRetry)
            assertTrue(failed.error.orEmpty().contains("Check the conversation"))
            repeat(3) { f.state.retry(SESSION_ID, failed.localId) }
            delay(200)
            assertEquals(1, f.posts.size, "legacy Gateway must receive at most one POST")
            assertEquals("possibly delivered", f.state.detailFor(SESSION_ID)?.pending?.single()?.text)
        }
    }

    @Test
    fun `同じセッションへの連続送信は先の送信完了まで待ち順序を保つ`(): Unit = runBlocking {
        Fixture(receipts = true, loseFirstResponse = false, gatedPost = 1).use { f ->
            f.waitForDetail()
            f.state.send(SESSION_ID, "first prompt")
            assertTrue(f.gateArrived.await(5, TimeUnit.SECONDS))
            f.state.send(SESSION_ID, "second prompt")
            delay(200)
            assertEquals(1, f.posts.size)
            f.releaseGate.countDown()
            withTimeout(5_000) {
                while (f.state.detailFor(SESSION_ID)?.pending?.count { it.state == SendState.ACCEPTED } != 2) delay(10)
            }
            assertEquals(listOf("first prompt", "second prompt"), f.posts.map {
                GatewayApi.json.decodeFromString<SendMessageRequest>(it).text
            })
        }
    }

    private class Fixture(
        receipts: Boolean,
        loseFirstResponse: Boolean,
        gatedPost: Int? = null,
    ) : AutoCloseable {
        val home = Files.createTempDirectory("desktop-delivery-test")
        val posts = CopyOnWriteArrayList<String>()
        val uploadedBodies = CopyOnWriteArrayList<String>()
        val uploadCount = AtomicInteger()
        val gateArrived = CountDownLatch(1)
        val releaseGate = CountDownLatch(1)
        private val server = MockWebServer()
        val state: DesktopAppState

        init {
            val session = """{"id":"$SESSION_ID","provider":"claude","status":"running","updatedAt":"2026-10-15T00:00:00Z","canSend":true,"paneId":"w1:p1"}"""
            server.dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse {
                    val path = request.url.encodedPath
                    if (request.method == "POST" && path == "/v1/sessions/$SESSION_ID/messages") {
                        posts += requireNotNull(request.body).utf8()
                        val number = posts.size
                        if (number == gatedPost) {
                            gateArrived.countDown()
                            check(releaseGate.await(5, TimeUnit.SECONDS))
                        }
                        if (number == 1 && loseFirstResponse) {
                            return MockResponse.Builder().onResponseStart(SocketEffect.CloseSocket()).build()
                        }
                        return MockResponse.Builder().code(202).body("{}").build()
                    }
                    if (request.method == "POST" && path == "/v1/uploads") {
                        uploadedBodies += requireNotNull(request.body).utf8()
                        val number = uploadCount.incrementAndGet()
                        return MockResponse.Builder().code(201).body("""{"id":"upload-$number"}""").build()
                    }
                    return when (path) {
                        "/v1/message-delivery" -> if (receipts) json("""{"idempotency":true}""") else MockResponse.Builder().code(404).body("not found").build()
                        "/v1/sessions" -> json("""{"sessions":[$session]}""")
                        "/v1/sessions/$SESSION_ID" -> json(session)
                        "/v1/sessions/$SESSION_ID/messages" -> json("""{"session":$session,"messages":[]}""")
                        "/v1/sessions/$SESSION_ID/seen" -> json("{}")
                        else -> MockResponse.Builder().code(404).build()
                    }
                }
            }
            server.start()
            val source = DesktopConnectionSource(home, emptyMap(), emptyMap())
            source.saveManual(DesktopManualConnection(server.url("/").toString(), "test-token"))
            // Use the production default client, including its connection
            // recovery settings, to catch transport-level replay as well.
            val http = OkHttpClient()
            state = DesktopAppState(source, GatewayApi(http) { source.current.settings }, http)
            state.openSession(SESSION_ID)
        }

        suspend fun waitForDetail() {
            withTimeout(5_000) {
                while (state.detailFor(SESSION_ID)?.session == null) delay(10)
            }
        }

        suspend fun waitForPending(wanted: SendState): PendingMessageUiState = withTimeout(5_000) {
            while (true) {
                state.detailFor(SESSION_ID)?.pending?.singleOrNull()?.takeIf { it.state == wanted }?.let { return@withTimeout it }
                delay(10)
            }
            error("unreachable")
        }

        override fun close() {
            releaseGate.countDown()
            state.close()
            server.close()
            home.toFile().deleteRecursively()
        }

        private fun json(body: String) = MockResponse.Builder().body(body).build()
    }

    private companion object {
        const val SESSION_ID = "session-test"
    }
}
