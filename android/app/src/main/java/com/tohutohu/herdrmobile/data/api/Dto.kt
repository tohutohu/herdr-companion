package com.tohutohu.herdrmobile.data.api

import kotlinx.serialization.Serializable

/** Mirrors gateway/internal/model. Unknown fields are ignored. */

@Serializable
data class SessionDto(
    val id: String,
    val provider: String,
    val providerName: String = provider,
    val project: String = "",
    val title: String? = null,
    val cwd: String? = null,
    val status: String,
    val updatedAt: String,
    val lastMessage: String? = null,
    val paneId: String? = null,
    val canSend: Boolean = false,
    /** Model the session last used, as the provider names it. */
    val model: String? = null,
    /** Reasoning effort: low, medium, high, ... */
    val effort: String? = null,
    /** Display label of the permission / collaboration mode. */
    val mode: String? = null,
    /** How full the context window is; null when the agent has not reported it. */
    val context: ContextUsageDto? = null,
    /** What the session's tokens are worth; null when nothing is known. */
    val cost: CostDto? = null,
    val archived: Boolean = false,
) {
    val isLive get() = status != Status.OFFLINE
}

/** How much of a session's context window its conversation currently fills. */
@Serializable
data class ContextUsageDto(
    val usedTokens: Long = 0,
    val windowTokens: Long = 0,
    val usedPercent: Int = 0,
)

/**
 * What a session cost at list API rates. [estimated] means the gateway priced
 * the token counts itself, because the agent reports no total while it runs.
 */
@Serializable
data class CostDto(val usd: Double = 0.0, val estimated: Boolean = false)

@Serializable
data class SessionsResponse(val sessions: List<SessionDto>)

@Serializable
data class MessageDto(
    val id: String,
    val role: String,
    val timestamp: String,
    val blocks: List<BlockDto> = emptyList(),
    /** Received by the agent but not taken into the conversation yet. */
    val queued: Boolean = false,
)

@Serializable
data class MessagesResponse(val session: SessionDto, val messages: List<MessageDto>)

@Serializable
data class BlockDto(
    val type: String,
    val text: String? = null,
    val url: String? = null,
    val path: String? = null,
    val line: Int? = null,
    val size: Long? = null,
    val interaction: InteractionDto? = null,
)

@Serializable
data class InteractionDto(
    val id: String,
    val type: String,
    /** Refines [type]; "plan" is a finished plan waiting for the go-ahead. */
    val kind: String? = null,
    val state: String,
    val title: String? = null,
    val detail: String? = null,
    val questions: List<QuestionDto> = emptyList(),
    val supported: Boolean = false,
    val answer: String? = null,
    val decisions: List<String> = emptyList(),
) {
    val isPending get() = state == "pending"
}

@Serializable
data class QuestionDto(
    val id: String,
    val type: String,
    val header: String? = null,
    val question: String,
    val options: List<OptionDto> = emptyList(),
    val allowOther: Boolean = false,
    /** Names the free-text choice when "Other" would be unclear. */
    val otherLabel: String? = null,
)

@Serializable
data class OptionDto(val label: String, val description: String? = null)

@Serializable
data class AnswerDto(val selected: List<String> = emptyList(), val text: String? = null)

@Serializable
data class InteractionResponseDto(
    val interactionId: String,
    val answers: Map<String, AnswerDto>? = null,
    val decision: String? = null,
)

@Serializable
data class SendMessageRequest(val text: String, val uploads: List<String> = emptyList())

@Serializable
data class UploadResponse(val id: String)

@Serializable
data class TerminalResponse(val paneId: String, val text: String, val revision: Long = 0)

@Serializable
data class TerminalInput(val text: String? = null, val keys: List<String> = emptyList())

@Serializable
data class FileEntryDto(val name: String, val path: String, val isDir: Boolean, val size: Long = 0)

@Serializable
data class FileInfoDto(
    val path: String,
    val name: String,
    val size: Long,
    val contentType: String,
    val previewable: Boolean,
)

@Serializable
data class FilesResponse(val root: String, val entries: List<FileEntryDto>)

/** One rate-limit window of a subscription (Claude's 5h / weekly, Codex's weekly). */
@Serializable
data class UsageWindowDto(
    val key: String = "",
    /** Window length, ready to display: "5h", "7d". */
    val label: String = "",
    /** Set when the window covers one model only, e.g. "Fable only". */
    val scope: String? = null,
    val usedPercent: Int = 0,
    val windowMinutes: Int = 0,
    val resetsAt: String? = null,
)

@Serializable
data class UsageProviderDto(
    val provider: String,
    val displayName: String = provider,
    val plan: String? = null,
    val account: String? = null,
    val windows: List<UsageWindowDto> = emptyList(),
    val updatedAt: String? = null,
    /** Only this provider could not be read. */
    val error: String? = null,
)

/** Providers keeps the last good reading even when [error] is set. */
@Serializable
data class UsageDto(
    val providers: List<UsageProviderDto> = emptyList(),
    val fetchedAt: String? = null,
    val error: String? = null,
)

@Serializable
data class DeviceRequest(val name: String, val fcmToken: String)

@Serializable
data class ErrorResponse(val error: String)

object Status {
    const val RUNNING = "running"
    const val WAITING_INPUT = "waiting_input"
    const val WAITING_APPROVAL = "waiting_approval"
    const val COMPLETED = "completed"
    const val IDLE = "idle"
    const val FAILED = "failed"
    const val OFFLINE = "offline"
}

@Serializable
data class DirEntryDto(val name: String, val path: String)

@Serializable
data class DirListingDto(val path: String = "", val parent: String? = null, val entries: List<DirEntryDto> = emptyList())

@Serializable
data class MkdirRequest(val parent: String, val name: String)

@Serializable
data class MkdirResponse(val path: String)

@Serializable
data class StartSessionRequest(
    val provider: String,
    val cwd: String,
    val prompt: String,
    val trust: Boolean,
    /** Null uses the agent's default model. */
    val model: String? = null,
    /** Null uses the agent's default reasoning effort. */
    val effort: String? = null,
)

@Serializable
data class ResumeRequest(val trust: Boolean)

/** A model or an effort the user can pick when starting a session. */
interface CatalogOption {
    val id: String
    val name: String
    val description: String?
    val default: Boolean
}

@Serializable
data class ModelOptionDto(
    override val id: String,
    override val name: String,
    override val description: String? = null,
    override val default: Boolean = false,
    /** Empty means the catalog's efforts apply. */
    val efforts: List<EffortOptionDto> = emptyList(),
) : CatalogOption

@Serializable
data class EffortOptionDto(
    override val id: String,
    override val name: String,
    override val description: String? = null,
    override val default: Boolean = false,
) : CatalogOption

@Serializable
data class ModelsResponse(
    val models: List<ModelOptionDto> = emptyList(),
    /** Efforts for a session started without a model. */
    val efforts: List<EffortOptionDto> = emptyList(),
)

@Serializable
data class StartSessionResponse(
    val sessionId: String? = null,
    val paneId: String,
    val warning: String? = null,
    /** The agent is asking whether to trust the folder; answer with [GatewayApi.answerTrust]. */
    val trustRequired: Boolean = false,
)

@Serializable
data class TrustAnswer(val trust: Boolean)

@Serializable
data class DirectoryCheckRequest(val cwd: String, val prompt: String)

@Serializable
data class DirectoryCheckResult(val verdict: String = "unknown", val historyCount: Int = 0)
