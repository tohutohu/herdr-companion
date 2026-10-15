package com.tohutohu.herdrcompanion.data.api

import com.tohutohu.herdrcompanion.data.Settings
import java.io.IOException
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicReference
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlinx.coroutines.runBlocking
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import mockwebserver3.SocketEffect
import okhttp3.OkHttpClient

class MessageDeliveryAttemptTest {
    @Test
    fun `受付応答が失われた再試行も同じ本文とrequestIdとuploadIdsを使う`() = runBlocking {
        MockWebServer().use { server ->
            val sent = CopyOnWriteArrayList<SendMessageRequest>()
            server.dispatcher = object : Dispatcher() {
                override fun dispatch(request: RecordedRequest): MockResponse {
                    if (request.method == "GET") return capability(true)
                    sent += GatewayApi.json.decodeFromString<SendMessageRequest>(request.body!!.utf8())
                    return if (sent.size == 1) lostResponse() else accepted()
                }
            }
            server.start()
            val api = api(server)
            val attempt = MessageDeliveryAttempt("stable-id", api)
            var uploads = 0
            assertFailsWith<IOException> { attempt.send(api, "s", "hello") { uploads++; listOf("upload-1") } }
            assertTrue(attempt.canRetry)
            attempt.send(api, "s", "hello") { uploads++; listOf("different-upload") }
            assertEquals(1, uploads)
            assertEquals(2, sent.size)
            assertEquals(sent[0], sent[1])
            assertEquals("stable-id", sent[0].requestId)
            assertEquals(listOf("upload-1"), sent[1].uploads)
        }
    }

    @Test
    fun `再試行前にGatewayがreceipt非対応へ戻ったら再送しない`() = runBlocking {
        MockWebServer().use { server ->
            server.enqueue(capability(true))
            server.enqueue(lostResponse())
            server.enqueue(capability(false))
            server.start()
            val api = api(server)
            val attempt = MessageDeliveryAttempt("stable-id", api)
            assertFailsWith<IOException> { attempt.send(api, "s", "hello") { emptyList() } }
            assertFailsWith<UnconfirmedMessageDeliveryException> { attempt.send(api, "s", "hello") { emptyList() } }
            assertFalse(attempt.canRetry)
            assertEquals(3, server.requestCount)
        }
    }

    @Test
    fun `Gatewayが結果不明を返した後はユーザーに履歴確認を求める`() = runBlocking {
        MockWebServer().use { server ->
            server.enqueue(capability(true))
            server.enqueue(MockResponse.Builder().code(409).body("""{"error":"Delivery unconfirmed"}""").build())
            server.start()
            val api = api(server)
            val attempt = MessageDeliveryAttempt("stable-id", api)
            assertFailsWith<UnconfirmedMessageDeliveryException> { attempt.send(api, "s", "hello") { emptyList() } }
            assertFalse(attempt.canRetry)
            assertFailsWith<UnconfirmedMessageDeliveryException> { attempt.send(api, "s", "hello") { emptyList() } }
            assertEquals(2, server.requestCount)
        }
    }

    @Test
    fun `能力照会失敗はPOST前なのでupload済みIDを保持して再試行できる`() = runBlocking {
        MockWebServer().use { server ->
            server.enqueue(MockResponse.Builder().code(503).build())
            server.enqueue(capability(true))
            server.enqueue(accepted())
            server.start()
            val api = api(server)
            val attempt = MessageDeliveryAttempt("stable-id", api)
            var uploads = 0
            assertFailsWith<GatewayException> { attempt.send(api, "s", "hello") { uploads++; listOf("upload-1") } }
            assertTrue(attempt.canRetry)
            attempt.send(api, "s", "hello") { uploads++; emptyList() }
            assertEquals(1, uploads)
            assertEquals(listOf("upload-1"), attempt.uploadIds)
        }
    }

    @Test
    fun `retry時に本文またはセッションを変えて同じIDを使わない`() = runBlocking {
        MockWebServer().use { server ->
            server.enqueue(capability(true))
            server.enqueue(lostResponse())
            server.start()
            val api = api(server)
            val attempt = MessageDeliveryAttempt("stable-id", api)
            assertFailsWith<IOException> { attempt.send(api, "s", "hello") { emptyList() } }
            assertFailsWith<IllegalStateException> { attempt.send(api, "s", "different") { emptyList() } }
            assertFailsWith<IllegalStateException> { attempt.send(api, "other", "hello") { emptyList() } }
            assertEquals(2, server.requestCount)
        }
    }

    @Test
    fun `upload中に接続設定が変わっても添付を新接続へ送り本文を実行しない`() = runBlocking {
        MockWebServer().use { original ->
            MockWebServer().use { replacement ->
                original.enqueue(MockResponse.Builder().code(201).body("""{"id":"upload-old"}""").build())
                original.start()
                replacement.start()
                val settings = AtomicReference(Settings(original.url("/").toString(), "old-secret"))
                val api = GatewayApi(OkHttpClient()) { settings.get() }
                val attempt = MessageDeliveryAttempt("stable-id", api)
                assertFailsWith<IOException> {
                    attempt.send(api, "s", "hello") { pinned ->
                        settings.set(Settings(replacement.url("/").toString(), "new-secret"))
                        listOf(pinned.upload("text/plain", "notes.txt") { "content".byteInputStream() })
                    }
                }
                assertFalse(attempt.canRetry)
                assertEquals(1, original.requestCount)
                val upload = original.takeRequest()
                assertEquals("/v1/uploads", upload.target)
                assertEquals("Bearer old-secret", upload.headers["Authorization"])
                assertEquals(0, replacement.requestCount)
            }
        }
    }

    @Test
    fun `メッセージPOSTは307リダイレクトで再送しない`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(MockResponse.Builder().code(307).addHeader("Location", server.url("/again")).build())
            assertFailsWith<GatewayException> { api(server).sendMessage("s", "hello", emptyList()) }
            assertEquals(1, server.requestCount)
        }
    }

    private fun api(server: MockWebServer) = GatewayApi(OkHttpClient()) { Settings(server.url("/").toString(), "secret") }
    private fun capability(enabled: Boolean) = MockResponse.Builder().body("""{"idempotency":$enabled}""").build()
    private fun accepted() = MockResponse.Builder().code(202).body("""{"ok":true}""").build()
    private fun lostResponse() = MockResponse.Builder().onResponseStart(SocketEffect.CloseSocket()).build()
}
