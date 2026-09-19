package com.tohutohu.herdrmobile.ui.detail

import android.app.Application
import android.net.Uri
import android.text.format.Formatter
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.tohutohu.herdrmobile.container
import com.tohutohu.herdrmobile.data.Attachment
import com.tohutohu.herdrmobile.data.PendingMessage
import com.tohutohu.herdrmobile.data.api.Status
import com.tohutohu.herdrmobile.data.db.SessionEntity
import com.tohutohu.herdrmobile.data.readAttachment
import com.tohutohu.herdrmobile.ui.sessions.SessionRef
import com.tohutohu.herdrmobile.ui.sessions.rememberSessionActions
import com.tohutohu.herdrmobile.ui.toUiModel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Android route: owns the VM, lifecycle polling, picker APIs and side effects. */
@Composable
fun SessionDetailRoute(
    sessionId: String,
    focusLatest: Boolean = false,
    onBack: () -> Unit,
    onOpenFile: (String, Int) -> Unit,
    onOpenImage: (String) -> Unit,
    onOpenTerminal: () -> Unit,
) {
    val context = LocalContext.current
    val vm: SessionDetailViewModel = viewModel(key = sessionId) {
        SessionDetailViewModel(context.applicationContext as Application, sessionId)
    }
    val api = context.container.api
    val session by vm.session.collectAsState()
    val messages by vm.messages.collectAsState()
    val error by vm.error.collectAsState()
    val pending by vm.pending.collectAsState()
    val answering by vm.answering.collectAsState()
    val actions = rememberSessionActions(onChanged = { vm.refresh() })
    actions.Dialogs()
    val lifecycle = LocalLifecycleOwner.current.lifecycle

    LaunchedEffect(lifecycle, sessionId) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) { vm.pollWhileVisible() }
    }

    val attachments = remember { mutableStateListOf<Attachment>() }
    val scope = rememberCoroutineScope()
    val resolver = context.contentResolver
    val add: (List<Uri>) -> Unit = { uris ->
        val fresh = uris.filterNot { uri -> attachments.any { it.uri == uri } }
        if (fresh.isNotEmpty()) {
            scope.launch {
                attachments.addAll(withContext(Dispatchers.IO) { fresh.map { readAttachment(resolver, it) } })
            }
        }
    }
    val imagePicker = rememberLauncherForActivityResult(ActivityResultContracts.PickMultipleVisualMedia(4), add)
    val filePicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments(), add)

    val uiState = SessionDetailUiState(
        sessionId = sessionId,
        session = session?.toUiModel(),
        messages = messages,
        pending = pending.map(PendingMessage::toUiState),
        error = error,
        answering = answering,
        attachments = attachments.map(Attachment::toUiState),
        actionBusy = session?.let { actions.busy(it.id) } == true,
    )

    SessionDetailScreen(
        state = uiState,
        focusLatest = focusLatest,
        resolveUrl = api::absolute,
        formatFileSize = { Formatter.formatShortFileSize(context, it) },
        resolveAttachmentPreview = { id -> attachments.firstOrNull { it.uri.toString() == id }?.uri },
        onAction = { action ->
            when (action) {
                SessionDetailAction.Back -> onBack()
                is SessionDetailAction.OpenFile -> onOpenFile(action.path, action.line)
                is SessionDetailAction.OpenImage -> onOpenImage(api.absolute(action.url))
                SessionDetailAction.OpenTerminal -> onOpenTerminal()
                SessionDetailAction.PickImage -> imagePicker.launch(
                    PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly),
                )
                SessionDetailAction.PickFile -> filePicker.launch(arrayOf("*/*"))
                is SessionDetailAction.RemoveAttachment -> attachments.removeAll { it.uri.toString() == action.id }
                is SessionDetailAction.Send -> {
                    vm.send(action.text, attachments.toList())
                    attachments.clear()
                }
                is SessionDetailAction.Retry -> vm.retry(action.localId)
                is SessionDetailAction.Discard -> vm.discard(action.localId)
                is SessionDetailAction.Respond -> vm.respond(action.response)
                SessionDetailAction.Resume -> session?.let { actions.resume(it.toSessionRef()) }
                SessionDetailAction.Archive -> session?.let { actions.archive(it.toSessionRef()) }
                SessionDetailAction.Unarchive -> session?.let { actions.unarchive(it.toSessionRef()) }
            }
        },
    )
}

private fun Attachment.toUiState() = AttachmentUiState(
    id = uri.toString(),
    name = name,
    mimeType = mime,
)

private fun PendingMessage.toUiState() = PendingMessageUiState(
    localId = localId,
    text = text,
    attachments = attachments.map(Attachment::toUiState),
    state = state,
    error = error,
)

private fun SessionEntity.toSessionRef() = SessionRef(id, live = status != Status.OFFLINE, archived = archived)
