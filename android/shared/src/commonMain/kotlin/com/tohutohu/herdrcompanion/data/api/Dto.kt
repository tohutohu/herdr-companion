package com.tohutohu.herdrcompanion.data.api

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
    /** The agent finished and the session has not been looked at since. */
    val unread: Boolean = false,
    /** Only in archive responses: why the session's worktree was kept. */
    val warning: String? = null,
    /**
     * A model or effort change the agent has not used yet; [settingsApply]
     * says when it will ("next_request" or "next_turn").
     */
    val pendingModel: String? = null,
    val pendingEffort: String? = null,
    val settingsApply: String? = null,
    /** Why the session failed. */
    val error: String? = null,
    /** Work the session left running, whatever [status] says. */
    val background: List<BackgroundTaskDto> = emptyList(),
    val originalTitle: String? = null,
    /** Effective translation mode; null when an older gateway has not reported it. */
    val translation: Boolean? = null,
) {
    val isLive get() = status != Status.OFFLINE
}

@Serializable
data class BackgroundTaskDto(
    val id: String,
    /** "agent", "shell" or "other". */
    val kind: String = "other",
    val description: String? = null,
    /** When the gateway first saw it. */
    val startedAt: String = "",
)

/** When a pending model or effort change takes effect, as [SessionDto.settingsApply] reports it. */
object SettingsApply {
    /** Claude: from the agent's next model request, possibly inside the running turn. */
    const val NEXT_REQUEST = "next_request"
    /** Codex: from the next turn. */
    const val NEXT_TURN = "next_turn"
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
data class SessionsResponse(
    val sessions: List<SessionDto>,
    /** Where the next archived page starts; absent on the last page. */
    val nextOffset: Int? = null,
)

@Serializable
data class ArchiveSessionsRequest(val ids: List<String>)

@Serializable
data class ArchiveSessionsResponse(val sessions: List<SessionDto>)

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

/** The pending interactions the app shows, for the gateway to check on reload. */
@Serializable
data class ReloadRequest(val shown: List<String>)

/** A question or approval a reload showed was missed; see docs/api.md. */
@Serializable
data class ReloadFindingDto(val kind: String, val message: String = "")

@Serializable
data class ReloadResponse(
    val session: SessionDto,
    val messages: List<MessageDto>,
    val findings: List<ReloadFindingDto> = emptyList(),
)

@Serializable
data class BlockDto(
    val type: String,
    val text: String? = null,
    val url: String? = null,
    val path: String? = null,
    val line: Int? = null,
    val size: Long? = null,
    val interaction: InteractionDto? = null,
    val sessionId: String? = null,
    val senderName: String? = null,
    val originalText: String? = null,
)

@Serializable
data class InteractionDto(
    val id: String,
    val type: String,
    /**
     * Refines [type]; "plan" is a finished plan waiting for the go-ahead.
     */
    val kind: String? = null,
    val state: String,
    val title: String? = null,
    val detail: String? = null,
    val questions: List<QuestionDto> = emptyList(),
    val supported: Boolean = false,
    val answer: String? = null,
    val decisions: List<String> = emptyList(),
    val translation: InteractionDto? = null,
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
data class OptionDto(
    val label: String,
    val description: String? = null,
    /** Monospace text, such as an ASCII mockup, to compare the options by. */
    val preview: String? = null,
    val value: String? = null,
)

@Serializable
data class AnswerDto(val selected: List<String> = emptyList(), val text: String? = null)

@Serializable
data class InteractionResponseDto(
    val interactionId: String,
    val answers: Map<String, AnswerDto>? = null,
    val decision: String? = null,
)

@Serializable
data class SendMessageRequest(val text: String, val uploads: List<String> = emptyList(), val requestId: String? = null)

@Serializable
data class SendMessageResponse(
    val ok: Boolean = true,
    /** Exact prompt sent after translation; absent on older gateways or receipt replays. */
    val sentText: String? = null,
)

@Serializable
data class MessageDeliveryCapabilities(val idempotency: Boolean = false)

@Serializable
data class UploadResponse(val id: String)

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
data class DirListingDto(
    val path: String = "",
    val parent: String? = null,
    val entries: List<DirEntryDto> = emptyList(),
    /** [path] is in a git repository, so a session can start in a new worktree. */
    val git: Boolean = false,
)

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
    /** Null starts the session in the agent's own mode. */
    val mode: String? = null,
    /** Starts in a new git worktree, removed again when the session is archived. */
    val worktree: Boolean = false,
    /** Upload ids attached to the first prompt. */
    val uploads: List<String> = emptyList(),
    /** Null keeps Gateway defaults; true translates prompts and display for this session. */
    val translation: Boolean? = null,
)

@Serializable
data class ResumeRequest(val trust: Boolean)

/** Body of the session model, effort and mode changes. */
@Serializable
data class SessionSettingRequest(val model: String? = null, val effort: String? = null, val mode: String? = null)

/** An earlier question and answer of a side conversation (btw). */
@Serializable
data class SideTurnDto(val question: String, val answer: String)

/** Body of `POST /v1/sessions/{id}/side-question`; [history] is oldest first. */
@Serializable
data class SideQuestionRequest(val question: String, val history: List<SideTurnDto> = emptyList())

/** [synthetic] marks an answer the agent made without the model, e.g. why it could not answer. */
@Serializable
data class SideAnswerDto(val answer: String, val synthetic: Boolean = false)

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
data class ModeOptionDto(
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
    /** Modes a new session can start in; empty means the agent offers none. */
    val modes: List<ModeOptionDto> = emptyList(),
)

@Serializable
data class StartSessionResponse(
    val sessionId: String? = null,
    /** Names a start waiting on the folder-trust answer. */
    val launchId: String? = null,
    val warning: String? = null,
    /**
     * The agent is asking whether to trust the folder; answer with
     * [GatewayApi.answerTrust] for [launchId].
     */
    val trustRequired: Boolean = false,
)

@Serializable
data class TrustAnswer(val trust: Boolean)

@Serializable
data class DirectoryCheckRequest(val cwd: String, val prompt: String)

@Serializable
data class DirectoryCheckResult(val verdict: String = "unknown", val historyCount: Int = 0)
