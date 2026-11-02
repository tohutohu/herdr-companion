package com.tohutohu.herdrcompanion.data.api

import com.tohutohu.herdrcompanion.data.Settings
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import java.io.IOException
import java.io.InputStream
import java.io.OutputStream
import java.net.URLEncoder
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.TimeUnit

/** [reason] is the gateway's error code when it sends one (resident API: invalid, conflict, trust_required, …). */
class GatewayException(val code: Int, message: String, val reason: String? = null) : IOException(message)

data class DownloadMetadata(
    val mimeType: String,
    val contentLength: Long,
)

/** A conditional response whose ETag becomes usable only after its consumer applies it. */
class MessageSnapshot internal constructor(
    val response: MessagesResponse,
    private val rememberTag: () -> Unit,
) {
    /** Call after updating the local messages; cancellation or a failed write must leave it unacknowledged. */
    fun acknowledge() = rememberTag()
}

/** Thin OkHttp client for the gateway HTTP API, shared by Android and Desktop JVM. */
class GatewayApi(
    private val http: OkHttpClient,
    private val settings: () -> Settings,
) {
    /** Pin sends and their uploads to the connection selected when they were queued. */
    internal fun connectionSettings() = settings()
    internal fun forConnection(connection: Settings) = GatewayApi(http) { connection }

    private val jsonType = "application/json".toMediaType()

    private fun base(): HttpUrl {
        val s = settings()
        return s.gatewayUrl.trim().trimEnd('/').toHttpUrlOrNull()
            ?: throw GatewayException(0, "Gateway URL is not configured")
    }

    /** Resolves a gateway-relative URL (e.g. image block URLs). */
    fun absolute(relative: String): String {
        if (relative.startsWith("http://") || relative.startsWith("https://")) return relative
        return settings().gatewayUrl.trim().trimEnd('/') + relative
    }

    private fun url(vararg segments: String, query: Map<String, String?> = emptyMap()): HttpUrl {
        val b = base().newBuilder()
        segments.forEach { b.addPathSegment(it) }
        query.forEach { (k, v) -> if (v != null) b.addQueryParameter(k, v) }
        return b.build()
    }

    private fun request(url: HttpUrl): Request.Builder =
        Request.Builder().url(url).header("Authorization", "Bearer ${settings().token.trim()}")

    private suspend fun execute(req: Request): String = withContext(Dispatchers.IO) {
        http.newCall(req).awaitBody { resp -> bodyOrThrow(resp) }
    }

    private fun bodyOrThrow(resp: Response): String {
        val body = resp.body.string()
        if (!resp.isSuccessful) {
            val err = runCatching { json.decodeFromString<ResidentErrorResponse>(body) }.getOrNull()
            throw GatewayException(resp.code, err?.error?.takeIf { it.isNotEmpty() } ?: "HTTP ${resp.code}", err?.code)
        }
        return body
    }

    private suspend inline fun <reified T> get(url: HttpUrl): T =
        json.decodeFromString(execute(request(url).get().build()))

    private suspend fun post(url: HttpUrl, body: RequestBody): String =
        execute(request(url).post(body).build())

    suspend fun sessions(): List<SessionDto> =
        get<SessionsResponse>(url("v1", "sessions")).sessions

    suspend fun session(id: String): SessionDto = get(url("v1", "sessions", id))

    /**
     * One page of archived sessions, most recently archived first. A limit of
     * 0 asks for the rest. Gateways without paging return every session and
     * no [SessionsResponse.nextOffset].
     */
    suspend fun archivedSessions(offset: Int, limit: Int): SessionsResponse =
        get(url("v1", "sessions", query = mapOf("archived" to "true", "offset" to "$offset", "limit" to "$limit")))

    /**
     * Archiving a running session stops its agent and removes the worktree it
     * ran in; [SessionDto.warning] says why one was kept.
     */
    suspend fun archive(id: String): SessionDto =
        json.decodeFromString(post(url("v1", "sessions", id, "archive"), ByteArray(0).toRequestBody(jsonType)))

    /**
     * Archives all [ids] in one gateway request. Running sessions are stopped
     * and their worktrees removed, one after another.
     */
    suspend fun archive(ids: List<String>): List<SessionDto> =
        slowPost<ArchiveSessionsResponse>(url("v1", "sessions", "archive"), json.encodeToString(ArchiveSessionsRequest(ids))).sessions

    suspend fun unarchive(id: String): SessionDto =
        json.decodeFromString(execute(request(url("v1", "sessions", id, "archive")).delete().build()))

    /** Reopens a stopped session. */
    suspend fun resume(id: String, trust: Boolean): StartSessionResponse =
        slowPost(url("v1", "sessions", id, "resume"), json.encodeToString(ResumeRequest(trust)))

    suspend fun messages(id: String, after: String? = null): MessagesResponse =
        get(url("v1", "sessions", id, "messages", query = mapOf("after" to after)))

    /** The ETag of each session's last applied messages response, and the URL it answered. */
    private val messageTags = ConcurrentHashMap<String, Pair<HttpUrl, String>>()

    /**
     * Like [messages], but returns null when the gateway reports the response
     * unchanged since this client last applied it for the same request.
     * A received response must be [acknowledged][MessageSnapshot.acknowledge]
     * after it has been applied, so cancellation cannot advance the ETag
     * past the consumer's cached messages.
     */
    suspend fun messagesIfChanged(id: String, after: String? = null): MessageSnapshot? {
        val url = url("v1", "sessions", id, "messages", query = mapOf("after" to after))
        val known = messageTags[id]?.takeIf { it.first == url }?.second
        val req = request(url).get().apply { if (known != null) header("If-None-Match", known) }.build()
        return withContext(Dispatchers.IO) {
            http.newCall(req).awaitBody { resp ->
                if (known != null && resp.code == 304) return@awaitBody null
                val body = json.decodeFromString<MessagesResponse>(bodyOrThrow(resp))
                val etag = resp.header("ETag")
                MessageSnapshot(body) {
                    if (etag != null) messageTags[id] = url to etag else messageTags.remove(id)
                }
            }
        }
    }

    /**
     * Reads the whole conversation again past the gateway's caches. [shown]
     * lists the pending interactions on screen, so the gateway can tell
     * whether polling missed one.
     */
    suspend fun reload(id: String, shown: List<String>): ReloadResponse =
        json.decodeFromString(
            post(
                url("v1", "sessions", id, "reload"),
                json.encodeToString(ReloadRequest(shown)).toRequestBody(jsonType),
            ),
        )

    /** Older gateways can send once, but cannot safely replay an ambiguous POST. */
    suspend fun supportsMessageIdempotency(): Boolean = try {
        get<MessageDeliveryCapabilities>(url("v1", "message-delivery")).idempotency
    } catch (error: GatewayException) {
        if (error.code == 404) false else throw error
    }

    suspend fun sendMessage(id: String, text: String, uploads: List<String>, requestId: String? = null) {
        val body = json.encodeToString(SendMessageRequest(text, uploads, requestId)).toRequestBody(jsonType)
        val once = object : RequestBody() {
            override fun contentType() = body.contentType()
            override fun contentLength() = body.contentLength()
            override fun isOneShot() = true
            override fun writeTo(sink: okio.BufferedSink) = body.writeTo(sink)
        }
        // Only the delivery owner may decide to retry a non-idempotent POST.
        // Old gateways ignore request IDs, so even transport-level replay is unsafe.
        execute(request(url("v1", "sessions", id, "messages")).post(once).build())
    }

    suspend fun respond(id: String, response: InteractionResponseDto) {
        post(url("v1", "sessions", id, "respond"), json.encodeToString(response).toRequestBody(jsonType))
    }

    suspend fun cycleMode(id: String) {
        post(url("v1", "sessions", id, "mode"), ByteArray(0).toRequestBody(jsonType))
    }

    /** Switches a session to the mode [mode] (a catalog mode id). */
    suspend fun setMode(id: String, mode: String) {
        post(url("v1", "sessions", id, "mode"), json.encodeToString(SessionSettingRequest(mode = mode)).toRequestBody(jsonType))
    }

    /**
     * Changes the model of a running session. The returned
     * session reports it as [SessionDto.pendingModel] until the agent uses it;
     * a model the agent does not offer is refused, never replaced.
     */
    suspend fun setModel(id: String, model: String): SessionDto =
        json.decodeFromString(
            post(url("v1", "sessions", id, "model"), json.encodeToString(SessionSettingRequest(model = model)).toRequestBody(jsonType)),
        )

    /** Like [setModel] for the effort; [model] names the model it is for when both change together. */
    suspend fun setEffort(id: String, effort: String, model: String? = null): SessionDto =
        json.decodeFromString(
            post(
                url("v1", "sessions", id, "effort"),
                json.encodeToString(SessionSettingRequest(model = model, effort = effort)).toRequestBody(jsonType),
            ),
        )

    /** Stops the session's running turn; the session stays. */
    suspend fun interrupt(id: String) {
        post(url("v1", "sessions", id, "interrupt"), ByteArray(0).toRequestBody(jsonType))
    }

    /**
     * Asks a side question (btw) about a Claude session: answered from its
     * conversation while a turn may run, and kept out of its history.
     */
    suspend fun sideQuestion(id: String, question: String, history: List<SideTurnDto>): SideAnswerDto = withContext(Dispatchers.IO) {
        val body = json.encodeToString(SideQuestionRequest(question, history)).toRequestBody(jsonType)
        val req = request(url("v1", "sessions", id, "side-question")).post(body).build()
        // The answer is a model call of its own; the gateway gives it up to three minutes.
        val slow = http.newBuilder().readTimeout(190, TimeUnit.SECONDS).callTimeout(200, TimeUnit.SECONDS).build()
        slow.newCall(req).awaitBody { resp -> json.decodeFromString(bodyOrThrow(resp)) }
    }

    /** Marks the session seen, which clears its unread state. */
    suspend fun markSeen(id: String) {
        post(url("v1", "sessions", id, "seen"), ByteArray(0).toRequestBody(jsonType))
    }

    /** Uploads one attachment and returns its upload id. */
    suspend fun upload(contentType: String, filename: String, size: Long? = null, openStream: () -> InputStream): String {
        val context = currentCoroutineContext()
        val body = StreamingUploadBody(contentType.toMediaType(), filename, size, openStream) { context.ensureActive() }
        val req = request(url("v1", "uploads"))
            .post(body)
            // RFC 5987, because OkHttp only accepts ASCII header values.
            .header("Content-Disposition", "attachment; filename*=UTF-8''" + encodeFilename(filename))
            .build()
        // Large files may take minutes over a tailnet; stalled writes still
        // fail within a minute instead of waiting for the full transfer budget.
        val uploads = http.newBuilder()
            .writeTimeout(60, TimeUnit.SECONDS)
            .callTimeout(30, TimeUnit.MINUTES)
            .build()
        return uploads.newCall(req).awaitBody { resp -> json.decodeFromString<UploadResponse>(bodyOrThrow(resp)).id }
    }

    suspend fun agentUpdates(): AgentUpdatesResponse = get(url("v1", "agents"))

    suspend fun updateAgent(provider: String): AgentUpdateDto =
        json.decodeFromString(post(url("v1", "agents", provider, "update"), "{}".toRequestBody(jsonType)))

    suspend fun files(id: String, path: String): FilesResponse =
        get(url("v1", "sessions", id, "files", query = mapOf("path" to path)))

    /** Returns content type and bytes. */
    suspend fun fileContent(id: String, path: String): Pair<String, ByteArray> = withContext(Dispatchers.IO) {
        val req = request(url("v1", "sessions", id, "files", "content", query = mapOf("path" to path))).get().build()
        http.newCall(req).awaitBody { resp ->
            if (!resp.isSuccessful) {
                val body = resp.body.string()
                val msg = runCatching { json.decodeFromString<ErrorResponse>(body).error }.getOrNull()
                    ?: "HTTP ${resp.code}"
                throw GatewayException(resp.code, msg)
            }
            (resp.header("Content-Type") ?: "application/octet-stream") to resp.body.bytes()
        }
    }

    /** What listens on the Mac's localhost, for opening it through the Gateway's proxy. */
    suspend fun localPorts(): LocalPortsResponse = get(url("v1", "ports"))

    suspend fun fileStat(id: String, path: String): FileInfoDto =
        get(url("v1", "sessions", id, "files", "stat", query = mapOf("path" to path)))

    /** Authenticated, range-capable endpoint for progressive media playback. */
    fun mediaRequest(id: String, path: String): Request =
        request(url("v1", "sessions", id, "files", "content", query = mapOf("path" to path, "download" to "1"))).get().build()

    /** Streams a file of any size into [out], reporting bytes written so far. */
    suspend fun downloadFile(id: String, path: String, out: OutputStream, onProgress: (Long) -> Unit) = withContext(Dispatchers.IO) {
        val req = request(url("v1", "sessions", id, "files", "content", query = mapOf("path" to path, "download" to "1"))).get().build()
        http.newCall(req).awaitBody { resp ->
            if (!resp.isSuccessful) bodyOrThrow(resp)
            resp.body.byteStream().use { input ->
                val buf = ByteArray(64 * 1024)
                var total = 0L
                while (true) {
                    ensureActive()
                    val n = input.read(buf)
                    if (n < 0) break
                    out.write(buf, 0, n)
                    total += n
                    onProgress(total)
                }
            }
        }
    }

    /** Streams an authenticated gateway URL and reports its response metadata. */
    suspend fun downloadUrl(
        rawUrl: String,
        out: OutputStream,
        onHeaders: (DownloadMetadata) -> Unit,
        onProgress: (Long) -> Unit,
    ): DownloadMetadata = withContext(Dispatchers.IO) {
        val target = absolute(rawUrl).toHttpUrlOrNull()
            ?: throw GatewayException(0, "Invalid download URL")
        val gateway = base()
        if (target.scheme != gateway.scheme || target.host != gateway.host || target.port != gateway.port) {
            throw GatewayException(0, "Download URL is not from the configured gateway")
        }
        val req = request(target).get().build()
        http.newCall(req).awaitBody { resp ->
            if (!resp.isSuccessful) {
                val body = resp.body.string()
                val msg = runCatching { json.decodeFromString<ErrorResponse>(body).error }.getOrNull()
                    ?: "HTTP ${resp.code}"
                throw GatewayException(resp.code, msg)
            }
            val metadata = DownloadMetadata(
                mimeType = resp.header("Content-Type")?.substringBefore(';')?.trim()
                    ?.takeIf { it.isNotEmpty() }
                    ?: "application/octet-stream",
                contentLength = resp.body.contentLength().coerceAtLeast(0),
            )
            onHeaders(metadata)
            resp.body.byteStream().use { input ->
                val buf = ByteArray(64 * 1024)
                var total = 0L
                while (true) {
                    ensureActive()
                    val n = input.read(buf)
                    if (n < 0) break
                    out.write(buf, 0, n)
                    total += n
                    onProgress(total)
                }
            }
            metadata
        }
    }

    suspend fun models(provider: String): ModelsResponse =
        get(url("v1", "models", query = mapOf("provider" to provider)))

    suspend fun directories(path: String?): DirListingDto =
        get(url("v1", "directories", query = mapOf("path" to path)))

    suspend fun createDirectory(parent: String, name: String): String {
        val body = post(url("v1", "directories"), json.encodeToString(MkdirRequest(parent, name)).toRequestBody(jsonType))
        return json.decodeFromString<MkdirResponse>(body).path
    }

    suspend fun checkDirectory(cwd: String, prompt: String): DirectoryCheckResult = withContext(Dispatchers.IO) {
        val body = json.encodeToString(DirectoryCheckRequest(cwd, prompt)).toRequestBody(jsonType)
        val req = request(url("v1", "directories", "check")).post(body).build()
        val bounded = http.newBuilder().callTimeout(12, TimeUnit.SECONDS).build()
        bounded.newCall(req).awaitBody { resp -> json.decodeFromString(bodyOrThrow(resp)) }
    }

    suspend fun startSession(body: StartSessionRequest): StartSessionResponse =
        slowPost(url("v1", "sessions"), json.encodeToString(body))

    /**
     * Continues (or, declined, cancels) a start that stopped at the
     * folder-trust dialog; [launch] is its [StartSessionResponse.launchId].
     */
    suspend fun answerTrust(launch: String, trust: Boolean): StartSessionResponse =
        slowPost(url("v1", "launches", launch, "trust"), json.encodeToString(TrustAnswer(trust)))

    /** Starting an agent can take up to a minute (startup dialogs, session id). */
    private suspend inline fun <reified T> slowPost(url: HttpUrl, body: String): T = withContext(Dispatchers.IO) {
        val req = request(url).post(body.toRequestBody(jsonType)).build()
        val slow = http.newBuilder().readTimeout(150, TimeUnit.SECONDS).callTimeout(160, TimeUnit.SECONDS).build()
        slow.newCall(req).awaitBody { resp -> json.decodeFromString(bodyOrThrow(resp)) }
    }

    /** Subscription limits as the gateway last read them. */
    suspend fun usage(): UsageDto = get(url("v1", "usage"))

    /** Re-reads the limits on the Mac; takes several seconds. */
    suspend fun refreshUsage(): UsageDto = slowPost(url("v1", "usage", "refresh"), "{}")

    suspend fun registerDevice(name: String, token: String) {
        post(url("v1", "devices"), json.encodeToString(DeviceRequest(name, token)).toRequestBody(jsonType))
    }

    suspend fun unregisterDevice(token: String) {
        execute(request(url("v1", "devices", query = mapOf("fcmToken" to token))).delete().build())
    }

    // --- resident tasks (常駐) ---

    /** What the gateway offers; a gateway too old to answer offers nothing beyond sessions. */
    suspend fun capabilities(): CapabilitiesDto = try {
        get(url("v1", "capabilities"))
    } catch (error: GatewayException) {
        if (error.code == 404) CapabilitiesDto() else throw error
    }

    private fun jsonBody(v: String) = v.toRequestBody(jsonType)

    /** Resident requests carry every value, defaults included: the gateway does not share the app's defaults (e.g. a manual schedule, notify on failure). */
    private inline fun <reified T> residentBody(v: T) = jsonBody(residentJson.encodeToString(v))

    suspend fun residentTasks(): List<ResidentTaskDto> = get<ResidentTasksResponse>(url("v1", "resident", "tasks")).tasks

    suspend fun residentTask(id: String): ResidentTaskDetailDto = get(url("v1", "resident", "tasks", id))

    suspend fun createResidentTask(body: ResidentTaskRequest): ResidentTaskDto =
        json.decodeFromString(post(url("v1", "resident", "tasks"), residentBody(body)))

    suspend fun updateResidentTask(id: String, body: ResidentTaskRequest): ResidentTaskDto =
        json.decodeFromString(execute(request(url("v1", "resident", "tasks", id)).patch(residentBody(body)).build()))

    suspend fun deleteResidentTask(id: String) {
        execute(request(url("v1", "resident", "tasks", id)).delete().build())
    }

    suspend fun setResidentEnabled(id: String, enabled: Boolean, until: String? = null): ResidentTaskDto =
        json.decodeFromString(
            if (enabled) post(url("v1", "resident", "tasks", id, "resume"), jsonBody("{}"))
            else post(url("v1", "resident", "tasks", id, "pause"), jsonBody(json.encodeToString(ResidentPauseRequest(until)))),
        )

    suspend fun runResidentNow(id: String, requestId: String): ResidentPostResultDto =
        json.decodeFromString(post(url("v1", "resident", "tasks", id, "runs"), jsonBody(json.encodeToString(ResidentRequestId(requestId)))))

    suspend fun residentMessages(id: String, limit: Int = 100): ResidentMessagesResponse =
        get(url("v1", "resident", "tasks", id, "messages", query = mapOf("limit" to "$limit")))

    suspend fun markResidentRead(id: String) {
        post(url("v1", "resident", "tasks", id, "read"), jsonBody(json.encodeToString(ResidentReadRequest())))
    }

    suspend fun residentRuns(id: String, limit: Int = 50): ResidentRunsResponse =
        get(url("v1", "resident", "tasks", id, "runs", query = mapOf("limit" to "$limit")))

    suspend fun residentRun(runId: String): ResidentRunDto = get(url("v1", "resident", "runs", runId))

    /** [action] is cancel (stop that run only) or dismiss (close a run whose outcome is unknown). */
    suspend fun residentRunAction(runId: String, action: String): ResidentRunDto = withContext(Dispatchers.IO) {
        // Stopping waits for the agent to end; allow for it.
        val req = request(url("v1", "resident", "runs", runId, action)).post(jsonBody("{}")).build()
        val slow = http.newBuilder().readTimeout(70, TimeUnit.SECONDS).callTimeout(80, TimeUnit.SECONDS).build()
        slow.newCall(req).awaitBody { resp -> json.decodeFromString(bodyOrThrow(resp)) }
    }

    /** Makes a run's conversation a session and resumes it; returns the session id. */
    suspend fun promoteResidentRun(runId: String): String = withContext(Dispatchers.IO) {
        // Resuming starts the agent; allow for it.
        val req = request(url("v1", "resident", "runs", runId, "promote")).post(jsonBody("{}")).build()
        val slow = http.newBuilder().readTimeout(100, TimeUnit.SECONDS).callTimeout(110, TimeUnit.SECONDS).build()
        slow.newCall(req).awaitBody { resp -> json.decodeFromString<ResidentPromotionDto>(bodyOrThrow(resp)).sessionId }
    }

    suspend fun answerResidentQuestion(questionId: String, text: String, requestId: String): ResidentPostResultDto =
        json.decodeFromString(post(url("v1", "resident", "questions", questionId, "answers"), jsonBody(json.encodeToString(ResidentTextRequest(text, requestId)))))

    suspend fun residentMemory(id: String): MemoryDto = get(url("v1", "resident", "tasks", id, "memory"))

    suspend fun putResidentMemory(id: String, body: MemoryUpdateRequest): MemoryDto =
        json.decodeFromString(execute(request(url("v1", "resident", "tasks", id, "memory")).put(residentBody(body)).build()))

    suspend fun residentArtifacts(id: String): List<ArtifactDto> =
        get<ArtifactsResponse>(url("v1", "resident", "tasks", id, "artifacts")).artifacts

    /** An artifact's content as text (files the gateway serves inline, up to its size limit). */
    suspend fun residentArtifactText(artifactId: String): String =
        execute(request(url("v1", "resident", "artifacts", artifactId, "content")).get().build())

    /** The newest notifications of every task, for clients that show them themselves. */
    suspend fun residentNotifications(limit: Int = 30): List<ResidentNotificationDto> =
        get<ResidentNotificationsResponse>(url("v1", "resident", "notifications", query = mapOf("limit" to "$limit"))).notifications

    suspend fun previewSchedule(schedule: ScheduleDto): SchedulePreviewDto =
        json.decodeFromString(post(url("v1", "resident", "schedule", "preview"), residentBody(SchedulePreviewRequest(schedule))))

    companion object {
        /** Percent-encodes a file name for the ext-value of Content-Disposition. */
        internal fun encodeFilename(name: String): String =
            URLEncoder.encode(name, "UTF-8").replace("+", "%20")

        val json = Json {
            ignoreUnknownKeys = true
            explicitNulls = false
            coerceInputValues = true
        }

        internal val residentJson = Json(json) { encodeDefaults = true }
    }
}
