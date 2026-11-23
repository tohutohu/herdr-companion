package com.tohutohu.herdrcompanion.data.api

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.channelFlow
import kotlinx.coroutines.flow.drop
import kotlinx.coroutines.flow.onCompletion
import kotlinx.coroutines.flow.shareIn
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import com.tohutohu.herdrcompanion.data.StreamingReply
import com.tohutohu.herdrcompanion.data.StreamingReplyState
import kotlinx.serialization.Serializable
import okhttp3.OkHttpClient
import java.io.IOException
import java.util.concurrent.TimeUnit

/** What the gateway's event stream (GET /v1/events, docs/api.md#イベント) reports. */
sealed interface GatewayEvent {
    /** The stream is live. [reset]: events were missed and cannot be replayed; refetch everything. */
    data class Ready(val reset: Boolean) : GatewayEvent

    /** A session the gateway runs changed: its state, summary or conversation. */
    data class SessionChanged(val sessionId: String, val status: String?, val removed: Boolean) : GatewayEvent

    /** The session list may have changed (archive, start, notes, …). */
    data object SessionsChanged : GatewayEvent

    /** Resident tasks or their runs changed. */
    data object ResidentChanged : GatewayEvent

    /** The whole text so far of a reply an agent is writing; [done] once it is closed. */
    data class Streaming(val sessionId: String, val messageId: String, val text: String, val done: Boolean) : GatewayEvent
}

@Serializable
private data class ReadyData(val reset: Boolean = false)

@Serializable
private data class SessionChangeData(val sessionId: String = "", val status: String? = null, val removed: Boolean = false)

@Serializable
private data class StreamingData(val sessionId: String = "", val messageId: String = "", val text: String = "", val done: Boolean = false)

/** Parses one event of the stream; null for types this client does not know. */
internal fun parseGatewayEvent(type: String, data: String): GatewayEvent? = runCatching {
    val json = GatewayApi.json
    when (type) {
        "ready" -> GatewayEvent.Ready(json.decodeFromString<ReadyData>(data).reset)
        "session" -> json.decodeFromString<SessionChangeData>(data).let { GatewayEvent.SessionChanged(it.sessionId, it.status, it.removed) }
        "sessions" -> GatewayEvent.SessionsChanged
        "resident" -> GatewayEvent.ResidentChanged
        "streaming" -> json.decodeFromString<StreamingData>(data).let { GatewayEvent.Streaming(it.sessionId, it.messageId, it.text, it.done) }
        else -> null
    }
}.getOrNull()

/** The gateway has no event stream (404): an older gateway. */
class EventsUnsupportedException : IOException("The gateway has no event stream")

/**
 * Reads the event stream once until it ends or fails. [lastEventId] is the
 * id of the last replayable event seen, for resuming; [onId] is told each new one.
 */
internal suspend fun GatewayApi.readEvents(
    http: OkHttpClient,
    lastEventId: String?,
    onId: (String) -> Unit,
    onEvent: suspend (GatewayEvent) -> Unit,
) {
    val req = eventsRequest(lastEventId)
    val call = http.newCall(req)
    val job = currentCoroutineContext()[Job]
    val cancel = job?.invokeOnCompletion { call.cancel() }
    try {
        val response = withContext(Dispatchers.IO) { call.execute() }
        response.use { resp ->
            if (resp.code == 404) throw EventsUnsupportedException()
            if (!resp.isSuccessful) throw GatewayException(resp.code, "HTTP ${resp.code}")
            val source = resp.body.source()
            var id: String? = null
            var type = "message"
            val data = StringBuilder()
            while (currentCoroutineContext().isActive) {
                val line = withContext(Dispatchers.IO) { source.readUtf8Line() } ?: break
                when {
                    line.isEmpty() -> {
                        if (data.isNotEmpty()) {
                            id?.let(onId)
                            parseGatewayEvent(type, data.toString())?.let { onEvent(it) }
                        }
                        id = null
                        type = "message"
                        data.clear()
                    }
                    line.startsWith(":") -> Unit
                    line.startsWith("id:") -> id = line.removePrefix("id:").trim()
                    line.startsWith("event:") -> type = line.removePrefix("event:").trim()
                    line.startsWith("data:") -> {
                        if (data.isNotEmpty()) data.append('\n')
                        data.append(line.removePrefix("data:").removePrefix(" "))
                    }
                }
            }
        }
    } finally {
        cancel?.dispose()
    }
}

/**
 * One connection to the gateway's event stream, shared by every screen that
 * listens: it is open while someone collects [events] (screens collect only
 * in the foreground) and reconnects with the last event id after a failure.
 * [connected] is true from the stream's ready event until it drops.
 */
class GatewayEventStream(
    private val api: GatewayApi,
    http: OkHttpClient,
    scope: CoroutineScope,
    private val retryDelays: List<Long> = listOf(1_000, 2_000, 5_000, 10_000, 30_000),
    /** How long to wait before asking a gateway without the stream again. */
    private val unsupportedDelay: Long = 5 * 60_000L,
) {
    // The gateway sends a comment every 20 s; a longer silence is a dead connection.
    private val streamHttp = http.newBuilder()
        .readTimeout(45, TimeUnit.SECONDS)
        .callTimeout(0, TimeUnit.SECONDS)
        .build()

    private val _connected = MutableStateFlow(false)
    val connected: StateFlow<Boolean> = _connected.asStateFlow()

    val events: SharedFlow<GatewayEvent> = channelFlow {
        var lastId: String? = null
        var failures = 0
        while (isActive) {
            val connection = api.connectionSettings()
            try {
                coroutineScope {
                    val reader = launch {
                        api.readEvents(streamHttp, lastId, onId = { lastId = it }) { event ->
                            if (event is GatewayEvent.Ready) {
                                _connected.value = true
                                failures = 0
                            }
                            send(event)
                        }
                    }
                    // Another gateway or token: leave this stream for the new one.
                    val watcher = launch {
                        while (api.connectionSettings() == connection) delay(SETTINGS_CHECK_MS)
                        reader.cancel()
                    }
                    reader.join()
                    watcher.cancel()
                }
                _connected.value = false
                // The gateway ended the stream (shutdown): reconnect soon.
                if (api.connectionSettings() == connection) delay(retryDelays.first())
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (_: EventsUnsupportedException) {
                _connected.value = false
                waitForChange(connection, unsupportedDelay)
            } catch (_: Exception) {
                _connected.value = false
                waitForChange(connection, retryDelays[failures.coerceAtMost(retryDelays.lastIndex)])
                failures++
            }
            if (api.connectionSettings() != connection) {
                lastId = null
                failures = 0
            }
        }
    }.onCompletion { _connected.value = false }
        .shareIn(scope, SharingStarted.WhileSubscribed(stopTimeoutMillis = 5_000), replay = 0)

    /** Waits [timeout], or less if the connection settings change. */
    private suspend fun waitForChange(connection: Any, timeout: Long) {
        withTimeoutOrNull(timeout) {
            while (api.connectionSettings() == connection) delay(SETTINGS_CHECK_MS)
        }
    }
}

/**
 * Runs [refresh] now and again whenever an event [relevant] to the caller
 * arrives, coalescing bursts, and otherwise every [period], or every
 * [connectedPeriod] while the event stream is connected (a safety net for
 * changes it does not report). Collecting keeps the stream open.
 */
suspend fun GatewayEventStream.pollWithEvents(
    period: Long,
    connectedPeriod: Long,
    relevant: (GatewayEvent) -> Boolean,
    refresh: suspend () -> Unit,
) = coroutineScope {
    val wake = Channel<Unit>(Channel.CONFLATED)
    launch { events.collect { if (relevant(it)) wake.trySend(Unit) } }
    // Losing the stream returns to the short period at once.
    launch { connected.drop(1).collect { if (!it) wake.trySend(Unit) } }
    while (true) {
        refresh()
        withTimeoutOrNull(if (connected.value) connectedPeriod else period) { wake.receive() }
        // Let a burst of events (a turn ending) settle into one fetch.
        delay(EVENT_COALESCE_MS)
        wake.tryReceive()
    }
}

private const val EVENT_COALESCE_MS = 100L
private const val SETTINGS_CHECK_MS = 2_000L

/** Events that mean the session list should be fetched again. */
fun GatewayEvent.affectsSessionList(): Boolean = when (this) {
    is GatewayEvent.Ready, is GatewayEvent.SessionChanged, GatewayEvent.SessionsChanged -> true
    else -> false
}

/** Events that mean the conversation of [sessionId] should be fetched again. */
fun GatewayEvent.affectsSession(sessionId: String): Boolean = when (this) {
    is GatewayEvent.Ready -> true
    is GatewayEvent.SessionChanged -> this.sessionId == sessionId
    is GatewayEvent.Streaming -> this.sessionId == sessionId && done
    else -> false
}

/**
 * The streamed replies of [sessionId], for showing them as they are written.
 * A (re)connection emits a closing event for nothing: streamed text is not
 * replayed, so a copy from before it may be stale ([StreamingReplyState.onEvent]
 * drops it).
 */
fun GatewayEventStream.streaming(sessionId: String): Flow<GatewayEvent.Streaming> = channelFlow {
    events.collect {
        when {
            it is GatewayEvent.Streaming && it.sessionId == sessionId -> send(it)
            it is GatewayEvent.Ready -> send(GatewayEvent.Streaming(sessionId, messageId = "", text = "", done = true))
        }
    }
}

/** Applies a streaming event of the gateway to [state]. */
fun StreamingReplyState.onEvent(event: GatewayEvent.Streaming, now: Long): StreamingReply? =
    if (event.messageId.isEmpty()) clear() else onEvent(event.messageId, event.text, event.done, now)
