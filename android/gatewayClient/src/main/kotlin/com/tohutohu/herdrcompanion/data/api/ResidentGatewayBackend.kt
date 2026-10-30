package com.tohutohu.herdrcompanion.data.api

import com.tohutohu.herdrcompanion.ui.resident.ResidentApiException
import com.tohutohu.herdrcompanion.ui.resident.ResidentBackend
import kotlinx.coroutines.CancellationException
import java.io.IOException
import java.time.Duration
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale
import java.util.UUID

/**
 * [ResidentBackend] over [GatewayApi], pinned to one connection. Gateway and
 * network errors become [ResidentApiException], so the screens can tell an
 * unreachable gateway, an old one and a refused request apart instead of
 * showing an empty list.
 */
class ResidentGatewayBackend(
    private val api: GatewayApi,
    override val connectionKey: String,
    private val zone: () -> ZoneId = { ZoneId.systemDefault() },
    private val now: () -> Instant = { Instant.now() },
) : ResidentBackend {

    private suspend fun <T> call(block: suspend GatewayApi.() -> T): T = try {
        api.block()
    } catch (e: CancellationException) {
        throw e
    } catch (e: GatewayException) {
        throw ResidentApiException(e.code, e.reason, e.message ?: "HTTP ${e.code}")
    } catch (e: IOException) {
        throw ResidentApiException(0, "unreachable", e.message ?: e.toString())
    }

    override suspend fun capabilities() = call { capabilities() }
    override suspend fun tasks() = call { residentTasks() }
    override suspend fun task(id: String) = call { residentTask(id) }
    override suspend fun create(request: ResidentTaskRequest) = call { createResidentTask(request) }
    override suspend fun update(id: String, request: ResidentTaskRequest) = call { updateResidentTask(id, request) }
    override suspend fun delete(id: String) = call { deleteResidentTask(id) }
    override suspend fun setEnabled(id: String, enabled: Boolean) = call { setResidentEnabled(id, enabled) }
    override suspend fun runNow(id: String, requestId: String) = call { runResidentNow(id, requestId) }
    override suspend fun messages(id: String, limit: Int) = call { residentMessages(id, limit) }
    override suspend fun postMessage(id: String, text: String, requestId: String) = call { postResidentMessage(id, text, requestId) }
    override suspend fun markRead(id: String) = call { markResidentRead(id) }
    override suspend fun runs(id: String, limit: Int) = call { residentRuns(id, limit) }
    override suspend fun run(runId: String) = call { residentRun(runId) }
    override suspend fun cancelRun(runId: String) = call { residentRunAction(runId, "cancel") }
    override suspend fun dismissRun(runId: String) = call { residentRunAction(runId, "dismiss") }
    override suspend fun answer(questionId: String, text: String, requestId: String) = call { answerResidentQuestion(questionId, text, requestId) }
    override suspend fun memory(id: String) = call { residentMemory(id) }
    override suspend fun putMemory(id: String, update: MemoryUpdateRequest) = call { putResidentMemory(id, update) }
    override suspend fun artifacts(id: String) = call { residentArtifacts(id) }
    override suspend fun artifactText(artifactId: String) = call { residentArtifactText(artifactId) }
    override suspend fun preview(schedule: ScheduleDto) = call { previewSchedule(schedule) }
    override suspend fun models(provider: String) = call { models(provider) }
    override suspend fun directories(path: String?) = call { directories(path) }

    override fun newRequestId(): String = UUID.randomUUID().toString()

    override fun formatInstant(iso: String): String = runCatching {
        val t = Instant.parse(iso).atZone(zone())
        val today = now().atZone(zone()).toLocalDate()
        val pattern = when {
            t.toLocalDate() == today -> "HH:mm"
            t.year == today.year -> "M/d (E) HH:mm"
            else -> "yyyy/M/d HH:mm"
        }
        t.format(DateTimeFormatter.ofPattern(pattern, Locale.JAPANESE))
    }.getOrDefault(iso)

    override fun relative(iso: String): String = runCatching { relativeText(Duration.between(now(), Instant.parse(iso))) }.getOrDefault(iso)

    companion object {
        /** "3分前" / "2時間後"; "まもなく" within a minute either way. */
        fun relativeText(d: Duration): String {
            val future = !d.isNegative
            val abs = d.abs()
            val amount = when {
                abs.toMinutes() < 1 -> return if (future) "まもなく" else "たった今"
                abs.toHours() < 1 -> "${abs.toMinutes()}分"
                abs.toDays() < 1 -> "${abs.toHours()}時間"
                else -> "${abs.toDays()}日"
            }
            return amount + if (future) "後" else "前"
        }
    }
}
