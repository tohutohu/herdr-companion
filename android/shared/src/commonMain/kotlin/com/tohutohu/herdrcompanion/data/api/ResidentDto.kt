package com.tohutohu.herdrcompanion.data.api

import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject

/** Mirrors gateway/internal/resident (常駐). Unknown fields are ignored. */

@Serializable
data class CapabilitiesDto(
    val messageIdempotency: Boolean = false,
    /** Null on a gateway without resident tasks. */
    val resident: ResidentCapabilityDto? = null,
)

@Serializable
data class ResidentCapabilityDto(
    val version: Int = 0,
    val providers: List<String> = emptyList(),
    val defaultTimezone: String = "Asia/Tokyo",
)

@Serializable
data class ScheduleDto(
    /** manual, once, daily, weekly or cron. */
    val kind: String = ScheduleKind.MANUAL,
    /** RFC 3339 instant of a one-off run. */
    val at: String? = null,
    /** HH:MM for daily and weekly. */
    val time: String? = null,
    /** mon..sun for weekly. */
    val days: List<String>? = null,
    val cron: String? = null,
    val timezone: String? = null,
)

object ScheduleKind {
    const val MANUAL = "manual"
    const val ONCE = "once"
    const val DAILY = "daily"
    const val WEEKLY = "weekly"
    const val CRON = "cron"
}

@Serializable
data class PermissionsDto(
    /** read_only or workspace_write. */
    val profile: String = PermissionProfile.WORKSPACE_WRITE,
    /** Extra Claude Code permission rules, e.g. Bash(gh pr view:*). */
    val allowedTools: List<String> = emptyList(),
)

object PermissionProfile {
    const val READ_ONLY = "read_only"
    const val WORKSPACE_WRITE = "workspace_write"
}

@Serializable
data class NotifyDto(
    /** changes, always or never. */
    val mode: String = "changes",
    val onFailure: Boolean = true,
    val onQuestion: Boolean = true,
    val cooldownMinutes: Int = 0,
    val quietStart: String? = null,
    val quietEnd: String? = null,
)

@Serializable
data class LimitsDto(
    val maxRunMinutes: Int = 30,
    val maxRunsPerDay: Int = 12,
    val expiresAt: String? = null,
)

/**
 * A command the gateway runs in the task's folder before an automatic run:
 * exit 0 starts the agent with its output, exit 1 ends the run without the
 * agent, anything else fails the run. An empty command removes it.
 */
@Serializable
data class PrecheckDto(
    val command: String = "",
    val timeoutSeconds: Int? = null,
)

@Serializable
data class RunBriefDto(
    val id: String,
    val status: String,
    val outcome: String? = null,
    val summary: String? = null,
    val error: String? = null,
    val startedAt: String? = null,
    val endedAt: String? = null,
)

@Serializable
data class ReportDto(
    val runId: String,
    val outcome: String = "",
    val summary: String = "",
    val at: String,
)

@Serializable
data class ResidentQuestionDto(
    val id: String,
    val taskId: String = "",
    val runId: String = "",
    val text: String,
    val options: List<String> = emptyList(),
    val status: String = "open",
    val createdAt: String = "",
    val expiresAt: String = "",
    val answer: String? = null,
)

@Serializable
data class ResidentTaskDto(
    val id: String,
    val name: String,
    val instruction: String = "",
    val provider: String,
    val model: String? = null,
    val effort: String? = null,
    val cwd: String = "",
    val schedule: ScheduleDto = ScheduleDto(),
    val permissions: PermissionsDto = PermissionsDto(),
    val notify: NotifyDto = NotifyDto(),
    val limits: LimitsDto = LimitsDto(),
    val precheck: PrecheckDto? = null,
    val enabled: Boolean = true,
    /** When a paused task resumes on its own; null while enabled or paused until resumed. */
    val pausedUntil: String? = null,
    val version: Long = 0,
    val createdAt: String = "",
    val updatedAt: String = "",
    val trustedAt: String? = null,
    /** idle, queued, running, waiting_user, needs_attention or paused. */
    val state: String = TaskState.IDLE,
    val nextRunAt: String? = null,
    val nextWakeReason: String? = null,
    val lastSuccessAt: String? = null,
    val lastRun: RunBriefDto? = null,
    val latestReport: ReportDto? = null,
    val pendingQuestion: ResidentQuestionDto? = null,
    val unread: Int = 0,
    val runsToday: Int = 0,
    val attention: String? = null,
)

object TaskState {
    const val IDLE = "idle"
    const val QUEUED = "queued"
    const val RUNNING = "running"
    const val WAITING_USER = "waiting_user"
    const val NEEDS_ATTENTION = "needs_attention"
    const val PAUSED = "paused"
}

@Serializable
data class ResidentTasksResponse(val tasks: List<ResidentTaskDto> = emptyList())

@Serializable
data class UsageSummaryDto(
    val inputTokens: Long = 0,
    val outputTokens: Long = 0,
    val cachedInputTokens: Long = 0,
    val costUsd: Double = 0.0,
    val durationMs: Long = 0,
    val numTurns: Int = 0,
)

@Serializable
data class AttemptDto(
    val id: String,
    val phase: String,
    val provider: String = "",
    val nativeId: String? = null,
    val model: String? = null,
    val startedAt: String = "",
    val endedAt: String? = null,
    val error: String? = null,
    val hasResult: Boolean = false,
)

@Serializable
data class ArtifactDto(
    val id: String,
    val taskId: String = "",
    val runId: String = "",
    val path: String,
    val title: String? = null,
    val createdAt: String = "",
)

@Serializable
data class ResidentRunDto(
    val id: String,
    val taskId: String = "",
    /** queued, running, completed, waiting_user, failed, cancelled, timed_out, needs_reconciliation, skipped, dismissed. */
    val status: String,
    val triggers: List<String> = emptyList(),
    val reason: String? = null,
    val queuedAt: String = "",
    val startedAt: String? = null,
    val endedAt: String? = null,
    val scheduledFor: String? = null,
    val outcome: String? = null,
    val summary: String? = null,
    val report: String? = null,
    val errorKind: String? = null,
    val error: String? = null,
    val provider: String? = null,
    val model: String? = null,
    val effort: String? = null,
    val usage: UsageSummaryDto? = null,
    val warnings: List<String> = emptyList(),
    val notificationKey: String? = null,
    val notifyDecision: String? = null,
    val nextWakeAt: String? = null,
    val interactive: Boolean = false,
    /** kept (can become a session), promoted ([sessionId]), or absent for runs from before they were kept. */
    val conversation: String? = null,
    val sessionId: String? = null,
    val attempts: List<AttemptDto> = emptyList(),
    val artifacts: List<ArtifactDto> = emptyList(),
    val question: ResidentQuestionDto? = null,
)

object RunStatus {
    const val QUEUED = "queued"
    const val RUNNING = "running"
    const val COMPLETED = "completed"
    const val WAITING_USER = "waiting_user"
    const val FAILED = "failed"
    const val CANCELLED = "cancelled"
    const val TIMED_OUT = "timed_out"
    const val NEEDS_RECONCILIATION = "needs_reconciliation"
    const val SKIPPED = "skipped"
    const val DISMISSED = "dismissed"
}

@Serializable
data class ResidentRunsResponse(val runs: List<ResidentRunDto> = emptyList(), val more: Boolean = false)

@Serializable
data class ResidentMessageDto(
    val id: String,
    val taskId: String = "",
    /** user, assistant or system. */
    val role: String,
    /** instruction, answer, report, question or notice. */
    val kind: String,
    val text: String,
    val runId: String? = null,
    val createdAt: String = "",
    val outcome: String? = null,
    val questionId: String? = null,
    /** The run's conversation, as on [ResidentRunDto]. */
    val conversation: String? = null,
    val sessionId: String? = null,
)

/** Whether a run's conversation can be continued as a session. */
fun canContinueAsSession(conversation: String?) = conversation == "kept" || conversation == "promoted"

@Serializable
data class ResidentPromotionDto(val sessionId: String)

@Serializable
data class ResidentMessagesResponse(val messages: List<ResidentMessageDto> = emptyList(), val more: Boolean = false)

@Serializable
data class MemoryDto(
    val taskId: String = "",
    val version: Long = 0,
    val summary: String = "",
    val state: JsonObject = JsonObject(emptyMap()),
    val updatedAt: String = "",
    val updatedBy: String? = null,
)

@Serializable
data class WakeupDto(
    val id: String,
    val kind: String,
    val dueAt: String,
    val reason: String? = null,
)

@Serializable
data class ResidentTaskDetailDto(
    val task: ResidentTaskDto,
    val memory: MemoryDto = MemoryDto(),
    val wakeups: List<WakeupDto> = emptyList(),
    val upcoming: List<String> = emptyList(),
    val recentRuns: List<ResidentRunDto> = emptyList(),
)

/** What creating or updating a task sends; null fields are left out. */
@Serializable
data class ResidentTaskRequest(
    val name: String? = null,
    val instruction: String? = null,
    val provider: String? = null,
    val model: String? = null,
    val effort: String? = null,
    val cwd: String? = null,
    val schedule: ScheduleDto? = null,
    val permissions: PermissionsDto? = null,
    val notify: NotifyDto? = null,
    val limits: LimitsDto? = null,
    /** Left out unless set or removed: gateways before prechecks refuse the field. */
    val precheck: PrecheckDto? = null,
    val enabled: Boolean? = null,
    val trust: Boolean? = null,
    /** Create only: makes a retried create return the same task. */
    val requestId: String? = null,
    /** Update only: the version the change is based on. */
    val expectedVersion: Long? = null,
)

@Serializable
data class ResidentPostResultDto(
    val message: ResidentMessageDto? = null,
    val run: ResidentRunDto,
    /** The request joined a run that was already queued. */
    val merged: Boolean = false,
)

@Serializable
data class SchedulePreviewRequest(val schedule: ScheduleDto, val count: Int = 3)

@Serializable
data class SchedulePreviewDto(val next: List<String> = emptyList(), val schedule: ScheduleDto? = null)

@Serializable
data class ResidentTextRequest(val text: String, val requestId: String? = null)

@Serializable
data class ResidentRequestId(val requestId: String? = null)

/** Pausing a task; [until] (RFC 3339) resumes it then, null keeps it paused. */
@Serializable
data class ResidentPauseRequest(val until: String? = null)

@Serializable
data class ResidentReadRequest(val messageId: String? = null)

@Serializable
data class MemoryUpdateRequest(
    val expectedVersion: Long,
    val summary: String? = null,
    val state: Map<String, JsonElement>? = null,
)

@Serializable
data class ArtifactsResponse(val artifacts: List<ArtifactDto> = emptyList())

@Serializable
data class ResidentErrorResponse(val error: String = "", val code: String? = null)

/** An outbox entry: a report the gateway decided to notify about. */
@Serializable
data class ResidentNotificationDto(
    val id: String,
    val taskId: String,
    val runId: String = "",
    /** change, question or failure. */
    val kind: String = "",
    val title: String = "",
    val body: String = "",
    val status: String = "",
    val createdAt: String = "",
    /** Not shown before this (quiet hours). */
    val notBefore: String = "",
)

@Serializable
data class ResidentNotificationsResponse(val notifications: List<ResidentNotificationDto> = emptyList())
