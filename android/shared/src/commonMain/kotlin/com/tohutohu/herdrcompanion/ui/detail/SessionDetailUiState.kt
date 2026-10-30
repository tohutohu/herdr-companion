package com.tohutohu.herdrcompanion.ui.detail

import com.tohutohu.herdrcompanion.data.Message
import com.tohutohu.herdrcompanion.data.SendState
import com.tohutohu.herdrcompanion.data.api.InteractionResponseDto
import com.tohutohu.herdrcompanion.data.api.ModelsResponse
import com.tohutohu.herdrcompanion.model.SessionUiModel

/** A picked or pending attachment in the form needed by the Compose UI. */
data class AttachmentUiState(
    val id: String,
    val name: String,
    val mimeType: String,
    val size: Long? = null,
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
    /** False when delivery is ambiguous and replay could duplicate the message. */
    val canRetry: Boolean = true,
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
    /**
     * Interactions the gateway accepted an answer for that the transcript may
     * still show as pending. Their controls stay disabled: answering again
     * would only be refused as no longer pending.
     */
    val answeredInteractions: Set<String> = emptySet(),
    val attachments: List<AttachmentUiState> = emptyList(),
    val actionBusy: Boolean = false,
    val modeChanging: Boolean = false,
    val loading: Boolean = false,
    /** A manual reload is reading the conversation again. */
    val reloading: Boolean = false,
    val reloadNotice: ReloadNotice? = null,
    /** Pulling the list past its newest message fetches it now (touch screens). */
    val pullToRefresh: Boolean = false,
    /** A fetch asked for by that pull is running. */
    val pullRefreshing: Boolean = false,
    val attachmentsEnabled: Boolean = true,
    val showBackButton: Boolean = true,
    val sendOnEnter: Boolean = false,
    val sendWithModifier: Boolean = false,
    /** Model / effort changes for a running session. */
    val settings: SessionSettingsUiState = SessionSettingsUiState(),
)

/**
 * The catalog a running session's model, effort and mode are picked from
 * (`GET /v1/models`), loaded when the details panel first shows the pickers.
 */
data class SessionSettingsUiState(
    /** Null until loaded. */
    val catalog: ModelsResponse? = null,
    val loading: Boolean = false,
    /** The catalog could not be loaded; the pickers stay hidden. */
    val loadError: String? = null,
    /** A change is on its way to the gateway. */
    val changing: Boolean = false,
    /** Why the gateway refused the last change, as it said it. */
    val changeError: String? = null,
)

/** User events emitted by the detail UI and handled by the Android route. */
sealed interface SessionDetailAction {
    data object Back : SessionDetailAction
    data class OpenFile(val path: String, val line: Int) : SessionDetailAction
    data class OpenImage(val url: String) : SessionDetailAction
    data class OpenSession(val sessionId: String) : SessionDetailAction
    data object PickImage : SessionDetailAction
    data object PickFile : SessionDetailAction
    data class RemoveAttachment(val id: String) : SessionDetailAction
    data class Send(val text: String) : SessionDetailAction
    data class Retry(val localId: String) : SessionDetailAction
    data class Discard(val localId: String) : SessionDetailAction
    data class Respond(val response: InteractionResponseDto) : SessionDetailAction
    data object CycleMode : SessionDetailAction
    /** Loads (or, after a failure, retries) the catalog for [SessionDetailUiState.settings]. */
    data object LoadSettings : SessionDetailAction
    data class SetModel(val model: String) : SessionDetailAction
    data class SetEffort(val effort: String) : SessionDetailAction
    data class SetMode(val mode: String) : SessionDetailAction
    /** Stops the running turn; the session stays. */
    data object Interrupt : SessionDetailAction
    data object Reload : SessionDetailAction
    data object PullRefresh : SessionDetailAction
    data object DismissReloadNotice : SessionDetailAction
    data object Resume : SessionDetailAction
    data object Archive : SessionDetailAction
    data object Unarchive : SessionDetailAction
}
