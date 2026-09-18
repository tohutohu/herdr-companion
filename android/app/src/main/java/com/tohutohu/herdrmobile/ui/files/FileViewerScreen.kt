package com.tohutohu.herdrmobile.ui.files

import android.text.format.Formatter
import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.SizeTransform
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.InsertDriveFile
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import com.tohutohu.herdrmobile.container
import com.tohutohu.herdrmobile.data.DownloadState
import com.tohutohu.herdrmobile.data.FileDownloads
import com.tohutohu.herdrmobile.data.api.FileInfoDto
import com.tohutohu.herdrmobile.ui.ExpandingContent

private sealed interface FileState {
    data object Loading : FileState
    data class Text(val lines: List<String>) : FileState
    data class Image(val bytes: ByteArray) : FileState
    data class Download(val info: FileInfoDto) : FileState
    data class Error(val message: String) : FileState
}

private const val MAX_LINES = 20_000

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun FileViewerScreen(sessionId: String, path: String, line: Int, onBack: () -> Unit) {
    val api = LocalContext.current.container.api
    var state by remember { mutableStateOf<FileState>(FileState.Loading) }
    LaunchedEffect(sessionId, path) {
        state = try {
            val info = api.fileStat(sessionId, path)
            if (!info.previewable) {
                FileState.Download(info)
            } else {
                val (type, bytes) = api.fileContent(sessionId, path)
                when {
                    type.startsWith("image/") -> FileState.Image(bytes)
                    type.startsWith("text/") -> FileState.Text(bytes.decodeToString().lines().take(MAX_LINES))
                    else -> FileState.Download(info)
                }
            }
        } catch (e: Exception) {
            FileState.Error(e.message ?: e.toString())
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                title = {
                    Text(path.substringAfterLast('/'), maxLines = 1)
                },
            )
        },
    ) { padding ->
        // The spinner fades into whatever the file turns out to be.
        AnimatedContent(
            targetState = state,
            contentKey = { it::class },
            transitionSpec = { fadeIn(tween(220)) togetherWith fadeOut(tween(150)) },
            modifier = Modifier.padding(padding).fillMaxSize(),
            label = "file",
        ) { s ->
            Box(Modifier.fillMaxSize()) {
                when (s) {
                    FileState.Loading -> CircularProgressIndicator(Modifier.align(Alignment.Center))
                    is FileState.Error -> Text(s.message, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(16.dp))
                    is FileState.Image -> AsyncImage(model = s.bytes, contentDescription = path, modifier = Modifier.fillMaxSize())
                    is FileState.Text -> CodeView(path, s.lines, line)
                    is FileState.Download -> DownloadView(sessionId, s.info)
                }
            }
        }
    }
}

@Composable
private fun DownloadView(sessionId: String, info: FileInfoDto) {
    val context = LocalContext.current
    val downloads = context.container.downloads
    val state by remember(sessionId, info.path) { downloads.state(sessionId, info.path) }.collectAsState()
    var openFailed by remember { mutableStateOf(false) }
    Column(
        Modifier.fillMaxSize().padding(24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
    ) {
        Icon(Icons.AutoMirrored.Filled.InsertDriveFile, contentDescription = null, modifier = Modifier.size(48.dp))
        Text(info.name, style = MaterialTheme.typography.titleMedium, textAlign = TextAlign.Center)
        Text(
            "${Formatter.formatFileSize(context, info.size)} · ${displayType(info)}",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Text(info.path, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace, textAlign = TextAlign.Center)
        // Each stage of the download fades into the next; progress catches up smoothly.
        AnimatedContent(
            targetState = state,
            contentKey = { it::class },
            transitionSpec = { fadeIn() togetherWith fadeOut() using SizeTransform(clip = false) },
            contentAlignment = Alignment.Center,
            label = "download",
        ) { s ->
            Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(12.dp)) {
                when (s) {
                    DownloadState.Idle -> Button(onClick = { downloads.start(sessionId, info.path, info.name, info.size) }) {
                        Text("Download")
                    }
                    is DownloadState.Running -> {
                        if (s.total > 0) {
                            val progress by animateFloatAsState(s.bytes.toFloat() / s.total, label = "downloadProgress")
                            LinearProgressIndicator(progress = { progress }, modifier = Modifier.fillMaxWidth())
                        } else {
                            LinearProgressIndicator(Modifier.fillMaxWidth())
                        }
                        Text(
                            "${Formatter.formatShortFileSize(context, s.bytes)} / ${Formatter.formatShortFileSize(context, s.total)}",
                            style = MaterialTheme.typography.bodySmall,
                        )
                    }
                    is DownloadState.Done -> {
                        Text("Saved to Downloads", style = MaterialTheme.typography.bodyMedium)
                        Button(onClick = { openFailed = !downloads.open(s) }) { Text("Open") }
                        ExpandingContent(value = "No app can open this file".takeIf { openFailed }) {
                            Text(it, color = MaterialTheme.colorScheme.error)
                        }
                    }
                    is DownloadState.Failed -> {
                        Text(s.message, color = MaterialTheme.colorScheme.error, textAlign = TextAlign.Center)
                        Button(onClick = { downloads.start(sessionId, info.path, info.name, info.size) }) { Text("Retry") }
                    }
                }
            }
        }
    }
}

/** The gateway sniffs content (an APK is a zip); the extension is more telling. */
private fun displayType(info: FileInfoDto): String =
    FileDownloads.mimeTypeFor(info.name).takeIf { it != "application/octet-stream" }
        ?: info.contentType.substringBefore(';')

@Composable
private fun CodeView(path: String, lines: List<String>, target: Int) {
    val listState = rememberLazyListState()
    LaunchedEffect(target, lines.size) {
        if (target > 0) listState.scrollToItem((target - 1 - 5).coerceIn(0, (lines.size - 1).coerceAtLeast(0)))
    }
    val digits = lines.size.toString().length
    val highlight = MaterialTheme.colorScheme.tertiaryContainer
    Box(Modifier.fillMaxSize().horizontalScroll(rememberScrollState())) {
        SelectionContainer {
            LazyColumn(state = listState, modifier = Modifier.width(2000.dp)) {
                item { Text(path, style = MaterialTheme.typography.labelSmall, modifier = Modifier.padding(8.dp)) }
                itemsIndexed(lines) { i, text ->
                    val n = i + 1
                    Row(
                        Modifier
                            .fillMaxWidth()
                            .then(if (n == target) Modifier.background(highlight) else Modifier),
                    ) {
                        Text(
                            n.toString().padStart(digits),
                            fontFamily = FontFamily.Monospace,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            textAlign = TextAlign.End,
                            modifier = Modifier.padding(horizontal = 8.dp),
                        )
                        Text(text.replace("\t", "    "), fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall, softWrap = false)
                    }
                }
            }
        }
    }
}
