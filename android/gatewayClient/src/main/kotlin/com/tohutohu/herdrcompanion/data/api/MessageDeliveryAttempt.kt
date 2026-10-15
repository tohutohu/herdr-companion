package com.tohutohu.herdrcompanion.data.api

import com.tohutohu.herdrcompanion.data.Settings
import java.io.IOException
import kotlinx.coroutines.CancellationException

class UnconfirmedMessageDeliveryException : IOException(
    "Delivery unconfirmed. Check the conversation before sending again.",
)

/** One logical message: retries retain its request identity, content and completed uploads. */
class MessageDeliveryAttempt(val requestId: String, api: GatewayApi) {
    private val connection: Settings = api.connectionSettings()
    private val pinnedApi = api.forConnection(connection)
    private var payload: Pair<String, String>? = null
    private var attempted = false
    private var receipts = false
    private var stopped = false

    var uploadIds: List<String>? = null
        private set

    val canRetry: Boolean get() = !stopped && (!attempted || receipts)

    /** Callers serialize a session and admit only one in-flight attempt per message. */
    suspend fun send(api: GatewayApi, sessionId: String, text: String, upload: suspend (GatewayApi) -> List<String>) {
        if (!canRetry) throw UnconfirmedMessageDeliveryException()
        checkConnection(api)
        val expected = sessionId to text
        check(payload == null || payload == expected) { "A message retry cannot change its content or session" }
        payload = expected
        val ids = uploadIds ?: upload(pinnedApi).toList().also { uploadIds = it }
        checkConnection(api)
        val supportsReceipts = pinnedApi.supportsMessageIdempotency()
        checkConnection(api)
        if (attempted && !supportsReceipts) {
            stopped = true
            throw UnconfirmedMessageDeliveryException()
        }
        // Record the attempt before the POST: cancellation can lose an accepted response.
        attempted = true
        receipts = supportsReceipts
        try {
            pinnedApi.sendMessage(sessionId, text, ids, requestId.takeIf { supportsReceipts })
        } catch (cancel: CancellationException) {
            throw cancel
        } catch (error: Exception) {
            if (!supportsReceipts || (error as? GatewayException)?.code == 409) {
                stopped = true
                throw UnconfirmedMessageDeliveryException()
            }
            throw error
        }
    }

    private fun checkConnection(api: GatewayApi) {
        if (api.connectionSettings() != connection) {
            stopped = true
            throw IOException("The gateway connection changed. Check the conversation before sending again.")
        }
    }
}
