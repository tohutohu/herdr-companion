package com.tohutohu.herdrmobile.ui.detail

import android.app.Application
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Image
import androidx.compose.material.icons.filled.Terminal
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import coil3.compose.AsyncImage
import com.tohutohu.herdrmobile.container
import com.tohutohu.herdrmobile.data.api.Status
import com.tohutohu.herdrmobile.data.db.SessionEntity
import com.tohutohu.herdrmobile.ui.agentSettingsLabel
import com.tohutohu.herdrmobile.ui.sessions.SessionRef
import com.tohutohu.herdrmobile.ui.sessions.rememberSessionActions
import com.tohutohu.herdrmobile.ui.statusStyle
import kotlinx.coroutines.launch

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SessionDetailScreen(
    sessionId: String,
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
    val actions = rememberSessionActions(onChanged = { vm.refresh() })
    actions.Dialogs()
    var menu by remember { mutableStateOf(false) }
    val messages by vm.messages.collectAsState()
    val error by vm.error.collectAsState()
    val sending by vm.sending.collectAsState()
    val lifecycle = LocalLifecycleOwner.current.lifecycle

    LaunchedEffect(lifecycle, sessionId) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) { vm.pollWhileVisible() }
    }

    // The list is laid out bottom-up, so it opens at the newest message and
    // growing items (streamed output, loading images) keep the bottom in place.
    val listState = rememberLazyListState()
    val lastId = messages.lastOrNull()?.id
    var prevLastId by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(lastId) {
        val prev = prevLastId
        prevLastId = lastId
        if (prev == null || lastId == null) return@LaunchedEffect
        // Follow new messages only while the previous newest one is on screen.
        val nearBottom = listState.layoutInfo.visibleItemsInfo.any { it.key == prev || it.key == lastId }
        if (nearBottom) listState.animateScrollToItem(0)
    }

    // Messages hidden above the viewport or under the pinned panel feed the panel.
    var panelHeight by remember { mutableIntStateOf(0) }
    val stack by remember(messages) {
        derivedStateOf {
            val info = listState.layoutInfo
            val bottomEdge = info.viewportEndOffset + info.afterContentPadding
            val top = info.visibleItemsInfo.filter { bottomEdge - it.offset > panelHeight }.maxOfOrNull { it.index }
            if (top == null) FlowStack.EMPTY else flowStack(messages, messages.lastIndex - top)
        }
    }
    val scope = rememberCoroutineScope()

    Scaffold(
        topBar = {
            TopAppBar(
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                title = {
                    Column {
                        Text(
                            listOfNotNull(session?.providerName, session?.project?.takeIf { it.isNotBlank() }).joinToString(" / ")
                                .ifBlank { sessionId },
                            style = MaterialTheme.typography.titleMedium,
                            maxLines = 1,
                        )
                        session?.let {
                            val st = statusStyle(it.status)
                            Row {
                                Text("${st.symbol} ${st.label}", color = st.color, style = MaterialTheme.typography.labelMedium)
                                agentSettingsLabel(it.model, it.effort, it.mode)?.let { label ->
                                    Text(
                                        " · $label",
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        style = MaterialTheme.typography.labelMedium,
                                        maxLines = 1,
                                    )
                                }
                            }
                        }
                    }
                },
                actions = {
                    IconButton(onClick = onOpenTerminal, enabled = session?.paneId != null) {
                        Icon(Icons.Default.Terminal, contentDescription = "Terminal")
                    }
                    session?.let { s ->
                        Box {
                            IconButton(onClick = { menu = true }) {
                                Icon(Icons.Default.MoreVert, contentDescription = "Session actions")
                            }
                            actions.Menu(s.ref(), menu) { menu = false }
                        }
                    }
                },
            )
        },
        bottomBar = {
            val s = session
            if (s != null && s.status == Status.OFFLINE) {
                ResumeBar(busy = actions.busy(s.id), onResume = { actions.resume(s.ref()) })
            } else {
                Composer(
                    enabled = s?.canSend == true && !sending,
                    sending = sending,
                    onSend = { text, images, clear -> vm.send(text, images, clear) },
                )
            }
        },
    ) { padding ->
        Column(Modifier.padding(padding).fillMaxSize()) {
            error?.let {
                Text(
                    it,
                    color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
                )
            }
            if (messages.isEmpty()) {
                Text("Loading…", modifier = Modifier.padding(16.dp))
            }
            Box(Modifier.fillMaxSize()) {
                LazyColumn(
                    state = listState,
                    modifier = Modifier.fillMaxSize(),
                    contentPadding = PaddingValues(vertical = 8.dp),
                    reverseLayout = true,
                ) {
                    itemsIndexed(messages.asReversed(), key = { _, m -> m.id }) { r, m ->
                        val i = messages.lastIndex - r
                        MessageItem(
                            message = m,
                            showRole = i == 0 || messages[i - 1].role != m.role,
                            providerName = session?.providerName ?: "Agent",
                            resolveUrl = api::absolute,
                            onOpenFile = onOpenFile,
                            onOpenImage = { onOpenImage(api.absolute(it)) },
                            onOpenTerminal = onOpenTerminal,
                            interactionsEnabled = !sending,
                            onRespond = vm::respond,
                        )
                    }
                }
                Box(Modifier.align(Alignment.TopCenter).onSizeChanged { panelHeight = it.height }) {
                    FlowStackPanel(
                        stack = stack,
                        providerName = session?.providerName ?: "Agent",
                        onJump = { id ->
                            val i = messages.indexOfFirst { it.id == id }
                            if (i >= 0) scope.launch { listState.animateScrollToItem(messages.lastIndex - i) }
                        },
                    )
                }
            }
        }
    }
}

@Composable
private fun Composer(
    enabled: Boolean,
    sending: Boolean,
    onSend: (String, List<Uri>, () -> Unit) -> Unit,
) {
    var text by rememberSaveable { mutableStateOf("") }
    val images = remember { mutableStateListOf<Uri>() }
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.PickMultipleVisualMedia(4)) { uris ->
        images.addAll(uris.filterNot { it in images })
    }
    Surface(tonalElevation = 3.dp) {
        Column(Modifier.navigationBarsPadding().imePadding().padding(8.dp)) {
            if (images.isNotEmpty()) {
                LazyRow(horizontalArrangement = Arrangement.spacedBy(8.dp), modifier = Modifier.padding(bottom = 8.dp)) {
                    items(images, key = { it.toString() }) { uri ->
                        Box {
                            AsyncImage(model = uri, contentDescription = null, modifier = Modifier.size(64.dp))
                            IconButton(onClick = { images.remove(uri) }, modifier = Modifier.size(24.dp).align(Alignment.TopEnd)) {
                                Icon(Icons.Default.Close, contentDescription = "Remove")
                            }
                        }
                    }
                }
            }
            Row(verticalAlignment = Alignment.CenterVertically) {
                IconButton(
                    enabled = enabled,
                    onClick = { picker.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly)) },
                ) { Icon(Icons.Default.Image, contentDescription = "Attach image") }
                OutlinedTextField(
                    value = text,
                    onValueChange = { text = it },
                    enabled = enabled,
                    placeholder = { Text(if (enabled || sending) "Message…" else "Session is not running in Herdr") },
                    maxLines = 6,
                    modifier = Modifier.weight(1f),
                )
                if (sending) {
                    CircularProgressIndicator(Modifier.padding(12.dp).size(24.dp))
                } else {
                    IconButton(
                        enabled = enabled && (text.isNotBlank() || images.isNotEmpty()),
                        onClick = {
                            onSend(text, images.toList()) {
                                text = ""
                                images.clear()
                            }
                        },
                    ) { Icon(Icons.AutoMirrored.Filled.Send, contentDescription = "Send") }
                }
            }
        }
    }
}

private fun SessionEntity.ref() = SessionRef(id, live = status != Status.OFFLINE, archived = archived)

/** Shown instead of the composer while the session is not running. */
@Composable
private fun ResumeBar(busy: Boolean, onResume: () -> Unit) {
    Surface(tonalElevation = 3.dp) {
        Row(
            Modifier.fillMaxWidth().navigationBarsPadding().padding(horizontal = 16.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                "Not running in Herdr",
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.weight(1f),
            )
            Button(onClick = onResume, enabled = !busy) {
                if (busy) {
                    CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                } else {
                    Icon(Icons.Default.PlayArrow, contentDescription = null)
                }
                Text("  Resume")
            }
        }
    }
}
