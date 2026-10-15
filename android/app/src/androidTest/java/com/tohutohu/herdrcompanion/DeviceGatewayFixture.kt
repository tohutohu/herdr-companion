package com.tohutohu.herdrcompanion

import com.tohutohu.herdrcompanion.data.Settings
import com.tohutohu.herdrcompanion.data.api.BlockDto
import com.tohutohu.herdrcompanion.data.api.GatewayApi
import com.tohutohu.herdrcompanion.data.api.MessageDto
import com.tohutohu.herdrcompanion.data.api.MessagesResponse
import com.tohutohu.herdrcompanion.data.api.SendMessageRequest
import com.tohutohu.herdrcompanion.data.api.SessionDto
import com.tohutohu.herdrcompanion.data.api.SessionsResponse
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.UUID
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest

/** Device-local HTTP fixture. It never reads a user's pairing credentials. */
internal class DeviceGatewayFixture : AutoCloseable {
    val sessionId = "claude:device-test"
    val gatewayId = "device-test-gateway"
    // WorkManager cancellation keeps historical WorkInfo rows. Each fixture
    // needs a fresh chain name so those rows cannot affect queue assertions.
    val connectionId = "device-test-${UUID.randomUUID()}"
    val transcript = "端末内 fixture の会話"
    val attempts = CopyOnWriteArrayList<SendMessageRequest>()
    val accepted = CopyOnWriteArrayList<SendMessageRequest>()
    val nextPostStatus = AtomicInteger(202)
    @Volatile var postGate: CountDownLatch? = null
    private val server = MockWebServer()
    private val session = SessionDto(sessionId, "claude", project = "Device test", title = "Device fixture session",
        status = "completed", updatedAt = "2026-10-15T00:00:00Z", canSend = true)

    init {
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                if (request.headers["Authorization"] != "Bearer device-test-token") return response(401, """{"error":"test token required"}""")
                val path = request.target.substringBefore('?')
                return when {
                    path == "/v1/message-delivery" -> response(200, """{"idempotency":true}""")
                    path == "/v1/sessions" -> response(200, GatewayApi.json.encodeToString(SessionsResponse(listOf(session))))
                    path == "/v1/sessions/$sessionId" -> response(200, GatewayApi.json.encodeToString(session))
                    path == "/v1/sessions/$sessionId/seen" -> response(200, "{}")
                    path == "/v1/sessions/$sessionId/messages" && request.method == "POST" -> {
                        val input = GatewayApi.json.decodeFromString<SendMessageRequest>(request.body!!.utf8())
                        attempts += input
                        val status = nextPostStatus.getAndSet(202)
                        check(postGate?.await(20, TimeUnit.SECONDS) != false) { "Test did not release the HTTP gate" }
                        if (status != 202) response(status, """{"error":"fixture delivery failure"}""")
                        else {
                            if (accepted.none { it.requestId == input.requestId }) accepted += input
                            response(202, """{"ok":true}""")
                        }
                    }
                    path == "/v1/sessions/$sessionId/messages" -> {
                        val messages = listOf(MessageDto("initial", "assistant", "2026-10-15T00:00:00Z", listOf(BlockDto("text", text = transcript)))) +
                            accepted.mapIndexed { index, input -> MessageDto("reply-$index", "user", "2026-10-15T00:00:01Z", listOf(BlockDto("text", text = input.text))) }
                        response(200, GatewayApi.json.encodeToString(MessagesResponse(session, messages)))
                    }
                    else -> response(404, """{"error":"fixture endpoint not found"}""")
                }
            }
        }
        server.start()
    }

    val settings get() = Settings(server.url("/").toString().trimEnd('/'), "device-test-token",
        connectionId = connectionId, gatewayId = gatewayId)

    override fun close() = server.close()

    private fun response(status: Int, body: String) = MockResponse.Builder().code(status)
        .addHeader("Content-Type", "application/json").body(body).build()
}
