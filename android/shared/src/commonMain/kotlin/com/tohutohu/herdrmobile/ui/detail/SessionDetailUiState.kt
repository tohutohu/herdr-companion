package com.tohutohu.herdrmobile.ui.detail

import com.tohutohu.herdrmobile.data.Message
import com.tohutohu.herdrmobile.data.SendState
import com.tohutohu.herdrmobile.data.api.InteractionResponseDto
import com.tohutohu.herdrmobile.model.SessionUiModel

/** A picked or pending attachment in the form needed by the Compose UI. */
data class AttachmentUiState(
    val id: String,
    val name: String,
    val mimeType: String,
) {
    val isImage get() = mimeType.startsWith("image/")
}

/** Pending-message presentation data without the Android content-provider type. */
data class PendingMessageUiState(
    val localId: String,
    val text: String,
    val attachments: List<AttachmentUiState>,
    val state: SendState,
    val error: String?,
)

/** The state required to render a session detail screen. */
data class SessionDetailUiState(
    val sessionId: String,
    val session: SessionUiModel?,
    val messages: List<Message>,
    val pending: List<PendingMessageUiState>,
    /** Stable keys for messages that replace an optimistic pending row. */
    val messageKeys: Map<String, String> = emptyMap(),
    val error: String?,
    val answering: Boolean,
    val attachments: List<AttachmentUiState> = emptyList(),
    val actionBusy: Boolean = false,
    val loading: Boolean = false,
    val attachmentsEnabled: Boolean = true,
    val showBackButton: Boolean = true,
    val sendOnEnter: Boolean = false,
    val sendWithModifier: Boolean = false,
)

/** User events emitted by the detail UI and handled by the Android route. */
sealed interface SessionDetailAction {
    data object Back : SessionDetailAction
    data class OpenFile(val path: String, val line: Int) : SessionDetailAction
    data class OpenImage(val url: String) : SessionDetailAction
    data object OpenTerminal : SessionDetailAction
    data object PickImage : SessionDetailAction
    data object PickFile : SessionDetailAction
    data class RemoveAttachment(val id: String) : SessionDetailAction
    data class Send(val text: String) : SessionDetailAction
    data class Retry(val localId: String) : SessionDetailAction
    data class Discard(val localId: String) : SessionDetailAction
    data class Respond(val response: InteractionResponseDto) : SessionDetailAction
    data object Resume : SessionDetailAction
    data object Archive : SessionDetailAction
    data object Unarchive : SessionDetailAction
}
