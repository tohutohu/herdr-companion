package com.tohutohu.herdrcompanion.ui.detail

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
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.tohutohu.herdrcompanion.container
import com.tohutohu.herdrcompanion.data.Attachment
import com.tohutohu.herdrcompanion.data.PendingMessage
import com.tohutohu.herdrcompanion.data.api.Status
import com.tohutohu.herdrcompanion.data.db.SessionEntity
import com.tohutohu.herdrcompanion.data.readAttachment
import com.tohutohu.herdrcompanion.ui.sessions.SessionRef
import com.tohutohu.herdrcompanion.ui.sessions.rememberSessionActions
import com.tohutohu.herdrcompanion.ui.toUiModel
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
    val messagePresentation by vm.messagePresentation.collectAsState()
    val error by vm.error.collectAsState()
    val loading by vm.loading.collectAsState()
    val answering by vm.answering.collectAsState()
    val modeChanging by vm.modeChanging.collectAsState()
    val reloading by vm.reloading.collectAsState()
    val reloadNotice by vm.reloadNotice.collectAsState()
    val actions = rememberSessionActions(onChanged = { vm.refresh() })
    actions.Dialogs()
    val lifecycle = LocalLifecycleOwner.current.lifecycle

    LaunchedEffect(lifecycle, sessionId) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) { vm.pollWhileVisible() }
    }

    val attachments = rememberPickedAttachments(sessionId)
    var attachmentError by rememberSaveable(sessionId) { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val resolver = context.contentResolver
    val add: (List<Uri>) -> Unit = { uris ->
        val fresh = uris.map(Uri::toString).filterNot { id -> attachments.any { it.id == id } }
        if (fresh.isNotEmpty()) {
            scope.launch {
                val result = withContext(Dispatchers.IO) {
                    readPickedAttachmentMetadata(fresh) { readAttachment(resolver, Uri.parse(it)).toUiState() }
                }
                attachments.addAll(result.attachments.filterNot { item -> attachments.any { it.id == item.id } })
                attachmentError = result.error
            }
        }
    }
    val imagePicker = rememberLauncherForActivityResult(ActivityResultContracts.PickMultipleVisualMedia(4), add)
    val filePicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments(), add)

    val uiState = SessionDetailUiState(
        sessionId = sessionId,
        session = session?.toUiModel(),
        messages = messagePresentation.messages,
        pending = messagePresentation.pending.map(PendingMessage::toUiState),
        messageKeys = messagePresentation.messageKeys,
        error = attachmentError ?: error,
        answering = answering,
        loading = loading,
        reloading = reloading,
        reloadNotice = reloadNotice,
        attachments = attachments.toList(),
        actionBusy = session?.let { actions.busy(it.id) } == true,
        modeChanging = modeChanging,
    )

    SessionDetailScreen(
        state = uiState,
        focusLatest = focusLatest,
        resolveUrl = api::absolute,
        formatFileSize = { Formatter.formatShortFileSize(context, it) },
        resolveAttachmentPreview = { id -> Uri.parse(id) },
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
                is SessionDetailAction.RemoveAttachment -> attachments.removeAll { it.id == action.id }
                is SessionDetailAction.Send -> {
                    vm.send(action.text, attachments.map { Attachment(Uri.parse(it.id), it.name, it.mimeType, it.size) })
                    attachments.clear()
                    attachmentError = null
                }
                is SessionDetailAction.Retry -> vm.retry(action.localId)
                is SessionDetailAction.Discard -> vm.discard(action.localId)
                is SessionDetailAction.Respond -> vm.respond(action.response)
                SessionDetailAction.CycleMode -> vm.cycleMode()
                SessionDetailAction.Reload -> vm.reload()
                SessionDetailAction.DismissReloadNotice -> vm.dismissReloadNotice()
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
    size = size,
)

private fun PendingMessage.toUiState() = PendingMessageUiState(
    localId = localId,
    text = text,
    attachments = attachments.map(Attachment::toUiState),
    state = state,
    error = error,
)

private fun SessionEntity.toSessionRef() = SessionRef(id, live = status != Status.OFFLINE, archived = archived)
