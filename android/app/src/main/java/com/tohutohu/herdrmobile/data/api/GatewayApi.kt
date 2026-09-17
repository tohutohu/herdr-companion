package com.tohutohu.herdrmobile.data.api

import com.tohutohu.herdrmobile.data.Settings
import kotlinx.coroutines.Dispatchers
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
import java.io.OutputStream
import java.util.concurrent.TimeUnit

class GatewayException(val code: Int, message: String) : IOException(message)

/** Thin OkHttp client for the gateway HTTP API. */
class GatewayApi(
    private val http: OkHttpClient,
    private val settings: () -> Settings,
) {
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
        http.newCall(req).execute().use { resp -> bodyOrThrow(resp) }
    }

    private fun bodyOrThrow(resp: Response): String {
        val body = resp.body.string()
        if (!resp.isSuccessful) {
            val msg = runCatching { json.decodeFromString<ErrorResponse>(body).error }.getOrNull()
                ?: "HTTP ${resp.code}"
            throw GatewayException(resp.code, msg)
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

    suspend fun archivedSessions(): List<SessionDto> =
        get<SessionsResponse>(url("v1", "sessions", query = mapOf("archived" to "true"))).sessions

    /** Archiving a running session closes its Herdr pane. */
    suspend fun archive(id: String): SessionDto =
        json.decodeFromString(post(url("v1", "sessions", id, "archive"), ByteArray(0).toRequestBody(jsonType)))

    suspend fun unarchive(id: String): SessionDto =
        json.decodeFromString(execute(request(url("v1", "sessions", id, "archive")).delete().build()))

    /** Reopens a stopped session in a new Herdr workspace. */
    suspend fun resume(id: String, trust: Boolean): StartSessionResponse =
        slowPost(url("v1", "sessions", id, "resume"), json.encodeToString(ResumeRequest(trust)))

    suspend fun messages(id: String, after: String? = null): MessagesResponse =
        get(url("v1", "sessions", id, "messages", query = mapOf("after" to after)))

    suspend fun sendMessage(id: String, text: String, uploads: List<String>) {
        post(
            url("v1", "sessions", id, "messages"),
            json.encodeToString(SendMessageRequest(text, uploads)).toRequestBody(jsonType),
        )
    }

    suspend fun respond(id: String, response: InteractionResponseDto) {
        post(url("v1", "sessions", id, "respond"), json.encodeToString(response).toRequestBody(jsonType))
    }

    suspend fun upload(bytes: ByteArray, contentType: String): String {
        val body = post(url("v1", "uploads"), bytes.toRequestBody(contentType.toMediaType()))
        return json.decodeFromString<UploadResponse>(body).id
    }

    suspend fun terminal(id: String, lines: Int = 300): TerminalResponse =
        get(url("v1", "sessions", id, "terminal", query = mapOf("lines" to lines.toString())))

    suspend fun terminalInput(id: String, input: TerminalInput) {
        post(url("v1", "sessions", id, "terminal"), json.encodeToString(input).toRequestBody(jsonType))
    }

    suspend fun files(id: String, path: String): FilesResponse =
        get(url("v1", "sessions", id, "files", query = mapOf("path" to path)))

    /** Returns content type and bytes. */
    suspend fun fileContent(id: String, path: String): Pair<String, ByteArray> = withContext(Dispatchers.IO) {
        val req = request(url("v1", "sessions", id, "files", "content", query = mapOf("path" to path))).get().build()
        http.newCall(req).execute().use { resp ->
            if (!resp.isSuccessful) {
                val body = resp.body.string()
                val msg = runCatching { json.decodeFromString<ErrorResponse>(body).error }.getOrNull()
                    ?: "HTTP ${resp.code}"
                throw GatewayException(resp.code, msg)
            }
            (resp.header("Content-Type") ?: "application/octet-stream") to resp.body.bytes()
        }
    }

    suspend fun fileStat(id: String, path: String): FileInfoDto =
        get(url("v1", "sessions", id, "files", "stat", query = mapOf("path" to path)))

    /** Streams a file of any size into [out], reporting bytes written so far. */
    suspend fun downloadFile(id: String, path: String, out: OutputStream, onProgress: (Long) -> Unit) = withContext(Dispatchers.IO) {
        val req = request(url("v1", "sessions", id, "files", "content", query = mapOf("path" to path, "download" to "1"))).get().build()
        http.newCall(req).execute().use { resp ->
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

    suspend fun models(provider: String): List<ModelOptionDto> =
        get<ModelsResponse>(url("v1", "models", query = mapOf("provider" to provider))).models

    suspend fun directories(path: String?): DirListingDto =
        get(url("v1", "directories", query = mapOf("path" to path)))

    suspend fun createDirectory(parent: String, name: String): String {
        val body = post(url("v1", "directories"), json.encodeToString(MkdirRequest(parent, name)).toRequestBody(jsonType))
        return json.decodeFromString<MkdirResponse>(body).path
    }

    suspend fun startSession(body: StartSessionRequest): StartSessionResponse =
        slowPost(url("v1", "sessions"), json.encodeToString(body))

    /** Starting an agent can take up to a minute (startup dialogs, session id). */
    private suspend inline fun <reified T> slowPost(url: HttpUrl, body: String): T = withContext(Dispatchers.IO) {
        val req = request(url).post(body.toRequestBody(jsonType)).build()
        val slow = http.newBuilder().readTimeout(150, TimeUnit.SECONDS).callTimeout(160, TimeUnit.SECONDS).build()
        slow.newCall(req).execute().use { resp -> json.decodeFromString(bodyOrThrow(resp)) }
    }

    suspend fun registerDevice(name: String, token: String) {
        post(url("v1", "devices"), json.encodeToString(DeviceRequest(name, token)).toRequestBody(jsonType))
    }

    companion object {
        val json = Json {
            ignoreUnknownKeys = true
            explicitNulls = false
            coerceInputValues = true
        }
    }
}
