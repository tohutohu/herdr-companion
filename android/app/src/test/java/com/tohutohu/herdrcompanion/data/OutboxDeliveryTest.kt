package com.tohutohu.herdrcompanion.data

import android.app.Application
import android.net.Uri
import androidx.room.Room
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import com.tohutohu.herdrcompanion.data.api.SendMessageRequest
import com.tohutohu.herdrcompanion.data.db.AppDatabase
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import mockwebserver3.SocketEffect
import okhttp3.OkHttpClient
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config

@RunWith(RobolectricTestRunner::class)
@Config(application = Application::class, sdk = [35])
class OutboxDeliveryTest {
    @Test
    fun `受付応答が失われてもretryは同じIDと添付を使い二重タップで増えない`() = runBlocking {
        val requests = CopyOnWriteArrayList<SendMessageRequest>()
        val receipts = HashSet<String>()
        val providerCalls = AtomicInteger()
        val uploads = AtomicInteger()
        val retryStarted = CountDownLatch(1)
        val releaseRetry = CountDownLatch(1)
        val dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                if (request.method == "GET") return capability(true)
                val payload = GatewayApi.json.decodeFromString<SendMessageRequest>(request.body!!.utf8())
                requests += payload
                synchronized(receipts) { if (receipts.add(payload.requestId!!)) providerCalls.incrementAndGet() }
                if (requests.size == 1) return lostResponse()
                retryStarted.countDown()
                check(releaseRetry.await(5, TimeUnit.SECONDS))
                return accepted()
            }
        }
        withOutbox(dispatcher, upload = { _, _ -> "upload-${uploads.incrementAndGet()}" }) { outbox ->
            try {
                outbox.send("s", "添付を確認して", listOf(Attachment(Uri.parse("content://test/file"), "notes.txt", "text/plain")), emptySet())
                val failed = await(outbox, SendState.FAILED)
                assertTrue(failed.canRetry)
                assertEquals(listOf("upload-1"), failed.uploadIds)
                outbox.retry(failed.localId)
                outbox.retry(failed.localId)
                assertTrue(retryStarted.await(5, TimeUnit.SECONDS))
                assertEquals(2, requests.size)
                releaseRetry.countDown()
                val done = await(outbox, SendState.ACCEPTED)
                assertEquals(failed.localId, done.localId)
                assertEquals(listOf("upload-1"), done.uploadIds)
                assertEquals(1, uploads.get())
                assertEquals(1, providerCalls.get())
                assertEquals(2, requests.size)
                assertEquals(requests[0], requests[1])
                assertEquals(failed.localId, requests[0].requestId)
            } finally {
                releaseRetry.countDown()
            }
        }
    }

    @Test
    fun `旧Gatewayへの曖昧な送信は手動retryでも二度送らない`() = runBlocking {
        val posts = AtomicInteger()
        val dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                if (request.method == "GET") return MockResponse.Builder().code(404).body("""{"error":"not found"}""").build()
                val payload = GatewayApi.json.decodeFromString<SendMessageRequest>(request.body!!.utf8())
                assertEquals(null, payload.requestId)
                posts.incrementAndGet()
                return lostResponse()
            }
        }
        withOutbox(dispatcher) { outbox ->
            outbox.send("s", "続けて", emptyList(), emptySet())
            val failed = await(outbox, SendState.FAILED)
            assertFalse(failed.canRetry)
            assertTrue(failed.error!!.contains("Check the conversation"))
            repeat(3) { outbox.retry(failed.localId) }
            assertEquals(failed, outbox.observe("s").first().single())
            assertEquals(1, posts.get())
        }
    }

    @Test
    fun `同一セッションの別メッセージは入力順を守り並列送信しない`() = runBlocking {
        val firstStarted = CountDownLatch(1)
        val releaseFirst = CountDownLatch(1)
        val sent = CopyOnWriteArrayList<String>()
        val dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                if (request.method == "GET") return capability(true)
                val payload = GatewayApi.json.decodeFromString<SendMessageRequest>(request.body!!.utf8())
                sent += payload.text
                if (payload.text == "first") {
                    firstStarted.countDown()
                    check(releaseFirst.await(5, TimeUnit.SECONDS))
                }
                return accepted()
            }
        }
        withOutbox(dispatcher) { outbox ->
            try {
                outbox.send("s", "first", emptyList(), emptySet())
                outbox.send("s", "second", emptyList(), emptySet())
                assertTrue(firstStarted.await(5, TimeUnit.SECONDS))
                assertEquals(listOf("first"), sent)
                releaseFirst.countDown()
                withTimeout(5_000) { outbox.observe("s").first { it.size == 2 && it.all { row -> row.state == SendState.ACCEPTED } } }
                assertEquals(listOf("first", "second"), sent)
            } finally {
                releaseFirst.countDown()
            }
        }
    }

    @Test
    fun `uploadが失敗した段階では旧Gatewayでも再試行できる`() = runBlocking {
        val uploadCalls = AtomicInteger()
        val posts = AtomicInteger()
        val dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                if (request.method == "GET") return MockResponse.Builder().code(404).build()
                posts.incrementAndGet()
                return accepted()
            }
        }
        withOutbox(dispatcher, upload = { _, _ ->
            if (uploadCalls.incrementAndGet() == 1) throw java.io.IOException("upload unavailable")
            "upload-ok"
        }) { outbox ->
            outbox.send("s", "with file", listOf(Attachment(Uri.parse("content://test/file"), "notes.txt", "text/plain")), emptySet())
            val failed = await(outbox, SendState.FAILED)
            assertTrue(failed.canRetry)
            assertEquals(0, posts.get())
            outbox.retry(failed.localId)
            assertNotNull(await(outbox, SendState.ACCEPTED))
            assertEquals(1, posts.get())
            assertEquals(2, uploadCalls.get())
        }
    }

    private suspend fun await(outbox: Outbox, state: SendState): PendingMessage = withTimeout(5_000) {
        outbox.observe("s").first { it.singleOrNull()?.state == state }.single()
    }

    private suspend fun withOutbox(
        dispatcher: Dispatcher,
        upload: suspend (GatewayApi, Attachment) -> String = { _, _ -> error("No attachments expected") },
        run: suspend (Outbox) -> Unit,
    ) {
        val context = RuntimeEnvironment.getApplication()
        val server = MockWebServer()
        server.dispatcher = dispatcher
        server.start()
        val db = Room.inMemoryDatabaseBuilder(context, AppDatabase::class.java).build()
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        try {
            val api = GatewayApi(OkHttpClient()) { Settings(server.url("/").toString(), "secret") }
            run(Outbox(context.contentResolver, api, SessionRepository(db, api), scope, upload, refresh = {}))
        } finally {
            scope.cancel()
            server.close()
            db.close()
        }
    }

    private fun accepted() = MockResponse.Builder().code(202).body("""{"ok":true}""").build()
    private fun capability(enabled: Boolean) = MockResponse.Builder().body("""{"idempotency":$enabled}""").build()
    private fun lostResponse() = MockResponse.Builder().onResponseStart(SocketEffect.CloseSocket()).build()
}
