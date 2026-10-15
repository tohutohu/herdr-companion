package com.tohutohu.herdrcompanion.data.api

import com.tohutohu.herdrcompanion.data.Settings
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient
import org.junit.Assert.*
import org.junit.Test

class GatewayDeliveryTest {
    @Test fun `新Gatewayは永続receipt機能を明示し返信IDを送る`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            val api = GatewayApi(OkHttpClient()) { Settings(server.url("/").toString(), "secret") }
            server.enqueue(MockResponse.Builder().body("""{"idempotency":true}""").build())
            assertTrue(api.supportsMessageIdempotency())
            assertEquals("/v1/message-delivery", server.takeRequest().target)
            server.enqueue(MockResponse.Builder().code(202).body("{}").build())
            api.sendMessage("claude:s1", "日本語🙂", emptyList(), requestId = "same-id")
            val body = GatewayApi.json.decodeFromString<SendMessageRequest>(server.takeRequest().body!!.utf8())
            assertEquals("same-id", body.requestId)
            assertEquals("日本語🙂", body.text)
        }
    }

    @Test fun `旧Gatewayの404は一回送信の互換経路となり既存送信形式を保つ`() = runBlocking {
        MockWebServer().use { server ->
            server.start()
            val api = GatewayApi(OkHttpClient()) { Settings(server.url("/").toString(), "secret") }
            server.enqueue(MockResponse.Builder().code(404).body("not found").build())
            assertFalse(api.supportsMessageIdempotency())
            server.takeRequest()
            server.enqueue(MockResponse.Builder().code(202).body("{}").build())
            api.sendMessage("claude:s1", "once", emptyList())
            val body = server.takeRequest().body!!.utf8()
            assertFalse(body.contains("requestId"))
        }
    }
}
