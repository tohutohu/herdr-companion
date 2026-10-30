package com.tohutohu.herdrcompanion.ui.resident

import com.tohutohu.herdrcompanion.data.api.ArtifactDto
import com.tohutohu.herdrcompanion.data.api.CapabilitiesDto
import com.tohutohu.herdrcompanion.data.api.DirListingDto
import com.tohutohu.herdrcompanion.data.api.MemoryDto
import com.tohutohu.herdrcompanion.data.api.MemoryUpdateRequest
import com.tohutohu.herdrcompanion.data.api.ModelsResponse
import com.tohutohu.herdrcompanion.data.api.ResidentMessagesResponse
import com.tohutohu.herdrcompanion.data.api.ResidentPostResultDto
import com.tohutohu.herdrcompanion.data.api.ResidentRunDto
import com.tohutohu.herdrcompanion.data.api.ResidentRunsResponse
import com.tohutohu.herdrcompanion.data.api.ResidentTaskDetailDto
import com.tohutohu.herdrcompanion.data.api.ResidentTaskDto
import com.tohutohu.herdrcompanion.data.api.ResidentTaskRequest
import com.tohutohu.herdrcompanion.data.api.ScheduleDto
import com.tohutohu.herdrcompanion.data.api.SchedulePreviewDto

/**
 * What the resident (常駐) screens need from the platform: the gateway API
 * and time formatting. Android and the Mac UI implement it over GatewayApi.
 * Every call talks to the connection the backend was made for; a backend
 * for another connection is a different instance, so tasks of two gateways
 * never mix.
 */
interface ResidentBackend {
    /** Identifies the connection, so screens drop state when it changes. */
    val connectionKey: String

    suspend fun capabilities(): CapabilitiesDto
    suspend fun tasks(): List<ResidentTaskDto>
    suspend fun task(id: String): ResidentTaskDetailDto
    suspend fun create(request: ResidentTaskRequest): ResidentTaskDto
    suspend fun update(id: String, request: ResidentTaskRequest): ResidentTaskDto
    suspend fun delete(id: String)
    /** Resumes, or pauses until [until] (RFC 3339; null: until resumed). */
    suspend fun setEnabled(id: String, enabled: Boolean, until: String? = null): ResidentTaskDto
    suspend fun runNow(id: String, requestId: String): ResidentPostResultDto
    suspend fun messages(id: String, limit: Int = 100): ResidentMessagesResponse
    suspend fun markRead(id: String)
    suspend fun runs(id: String, limit: Int = 50): ResidentRunsResponse
    suspend fun run(runId: String): ResidentRunDto
    suspend fun cancelRun(runId: String): ResidentRunDto
    suspend fun dismissRun(runId: String): ResidentRunDto
    /** Continues a run's conversation as a session (resumed); returns its id. */
    suspend fun promoteRun(runId: String): String
    suspend fun answer(questionId: String, text: String, requestId: String): ResidentPostResultDto
    suspend fun memory(id: String): MemoryDto
    suspend fun putMemory(id: String, update: MemoryUpdateRequest): MemoryDto
    suspend fun artifacts(id: String): List<ArtifactDto>
    suspend fun artifactText(artifactId: String): String
    suspend fun preview(schedule: ScheduleDto): SchedulePreviewDto
    suspend fun models(provider: String): ModelsResponse
    suspend fun directories(path: String?): DirListingDto

    /** A new idempotency key for one logical request (kept across its retries). */
    fun newRequestId(): String

    /** An RFC 3339 instant as a local date and time, e.g. "11/02 (月) 07:00". */
    fun formatInstant(iso: String): String

    /** An instant relative to now, e.g. "3分前", "2時間後". */
    fun relative(iso: String): String

    /** When a pause of [length] chosen now ends (RFC 3339), null for one until resumed. */
    fun pauseEnd(length: PauseLength): String?
}

/** The pause lengths the task screen offers. */
enum class PauseLength(val label: String) {
    ONE_HOUR("1時間"),
    THREE_HOURS("3時間"),
    TOMORROW_MORNING("明日の朝 7:00 まで"),
    ONE_WEEK("1週間"),
    UNTIL_RESUMED("再開するまで"),
}

/**
 * A gateway error the screens act on. [code] is the gateway's resident
 * error code (not_found, invalid, conflict, trust_required, …); [status]
 * the HTTP status, 0 when the gateway could not be reached.
 */
class ResidentApiException(val status: Int, val code: String?, message: String) : Exception(message) {
    val unreachable get() = status == 0
    val trustRequired get() = code == "trust_required"
    val conflict get() = code == "conflict"
    val notFound get() = status == 404
}
