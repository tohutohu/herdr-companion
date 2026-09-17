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
)

@Serializable
data class SessionsResponse(val sessions: List<SessionDto>)

@Serializable
data class MessageDto(
    val id: String,
    val role: String,
    val timestamp: String,
    val blocks: List<BlockDto> = emptyList(),
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
    val interaction: InteractionDto? = null,
)

@Serializable
data class InteractionDto(
    val id: String,
    val type: String,
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
data class FilesResponse(val root: String, val entries: List<FileEntryDto>)

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
)

@Serializable
data class ModelOptionDto(
    val id: String,
    val name: String,
    val description: String? = null,
    val default: Boolean = false,
)

@Serializable
data class ModelsResponse(val models: List<ModelOptionDto> = emptyList())

@Serializable
data class StartSessionResponse(val sessionId: String? = null, val paneId: String, val warning: String? = null)
