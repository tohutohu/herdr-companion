package com.tohutohu.herdrcompanion.data.api

import com.tohutohu.herdrcompanion.data.Settings
import java.io.ByteArrayInputStream
import java.io.InputStream
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeout
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient

class StreamingUploadApiTest {
    @Test
    fun `既知サイズはContent-Lengthと元の内容を送信しストリームを閉じる`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(MockResponse.Builder().code(201).body("""{"id":"upload1"}""").build())
            val api = GatewayApi(OkHttpClient()) { Settings(server.url("/").toString(), "secret") }
            var opens = 0
            var closed = false
            val source = object : ByteArrayInputStream("hello".toByteArray()) {
                override fun close() { closed = true; super.close() }
            }
            assertFalse(closed)
            assertEquals("upload1", api.upload("text/plain", "hello.txt", 5) { opens++; source })
            val request = server.takeRequest()
            assertEquals("5", request.headers["Content-Length"])
            assertNull(request.headers["Transfer-Encoding"])
            assertEquals("hello", request.body!!.utf8())
            assertEquals("Bearer secret", request.headers["Authorization"])
            assertEquals(1, opens)
            assertTrue(closed)
        }
    }

    @Test
    fun `サイズ不明のプロバイダーはchunked送信しリダイレクトで再読み取りしない`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(MockResponse.Builder().code(307).addHeader("Location", server.url("/again")).build())
            val api = GatewayApi(OkHttpClient()) { Settings(server.url("/").toString(), "secret") }
            var opens = 0
            val failure = runCatching { api.upload("text/plain", "hello.txt") { opens++; "hello".byteInputStream() } }.exceptionOrNull()
            assertTrue(failure is GatewayException)
            assertEquals(307, failure.code)
            val request = server.takeRequest()
            assertEquals("chunked", request.headers["Transfer-Encoding"])
            assertEquals("hello", request.body!!.utf8())
            assertEquals(1, opens)
            assertEquals(1, server.requestCount)
        }
    }

    @Test
    fun `送信中のキャンセルは次の読み取りを止めストリームを閉じる`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            server.enqueue(MockResponse.Builder().code(201).body("""{"id":"unused"}""").build())
            val api = GatewayApi(OkHttpClient()) { Settings(server.url("/").toString(), "secret") }
            val started = CompletableDeferred<Unit>()
            val closed = CompletableDeferred<Unit>()
            val release = CountDownLatch(1)
            var reads = 0
            val source = object : InputStream() {
                override fun read(): Int = error("bulk reads expected")
                override fun read(bytes: ByteArray, offset: Int, length: Int): Int {
                    reads++
                    started.complete(Unit)
                    check(release.await(5, TimeUnit.SECONDS))
                    bytes.fill(0, offset, offset + length)
                    return length
                }
                override fun close() { closed.complete(Unit) }
            }
            val sending = launch { api.upload("application/octet-stream", "large.bin") { source } }
            try {
                withTimeout(5_000) { started.await() }
                sending.cancelAndJoin()
            } finally {
                release.countDown()
            }
            withTimeout(5_000) { closed.await() }
            assertEquals(1, reads)
        }
    }
}
