package com.tohutohu.herdrcompanion.ui.files

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
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.InsertDriveFile
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Download
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import com.tohutohu.herdrcompanion.container
import com.tohutohu.herdrcompanion.data.DownloadState
import com.tohutohu.herdrcompanion.data.FileDownloads
import com.tohutohu.herdrcompanion.data.api.FileInfoDto
import com.tohutohu.herdrcompanion.ui.ExpandingContent
import com.tohutohu.herdrcompanion.ui.markdown.CodeToken
import com.tohutohu.herdrcompanion.ui.markdown.HighlightedCodeLine
import com.tohutohu.herdrcompanion.ui.markdown.MarkdownText
import com.tohutohu.herdrcompanion.ui.markdown.highlightCodeLines
import com.tohutohu.herdrcompanion.ui.markdown.languageForPath
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.ui.unit.Dp
import com.tohutohu.herdrcompanion.ui.exceptBottom

private sealed interface FileState {
    data object Loading : FileState
    data class Text(
        val lines: List<String>,
        val markdown: Boolean,
        val htmlInfo: FileInfoDto? = null,
    ) : FileState
    data class Image(val bytes: ByteArray) : FileState
    data class Media(val info: FileInfoDto) : FileState
    data class Download(val info: FileInfoDto) : FileState
    data class Error(val message: String) : FileState
}

private const val MAX_LINES = 20_000
private val MARKDOWN_EXTENSIONS = setOf("md", "markdown", "mdown", "mkdn", "mdwn")
private val HTML_EXTENSIONS = setOf("htm", "html")

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun FileViewerScreen(sessionId: String, path: String, line: Int, onBack: () -> Unit) {
    val context = LocalContext.current
    val api = context.container.api
    val downloads = context.container.downloads
    val downloadState by remember(sessionId, path) { downloads.state(sessionId, path) }.collectAsState()
    var info by remember(sessionId, path) { mutableStateOf<FileInfoDto?>(null) }
    var state by remember(sessionId, path) { mutableStateOf<FileState>(FileState.Loading) }
    LaunchedEffect(sessionId, path) {
        state = try {
            val loaded = api.fileStat(sessionId, path)
            info = loaded
            if (displayType(loaded).let { it.startsWith("video/") || it.startsWith("audio/") || it == "application/ogg" }) {
                FileState.Media(loaded)
            } else if (!loaded.previewable) {
                FileState.Download(loaded)
            } else {
                val (type, bytes) = api.fileContent(sessionId, path)
                when {
                    type.startsWith("image/") -> FileState.Image(bytes)
                    type.startsWith("text/") -> FileState.Text(
                        lines = bytes.decodeToString().lines().take(MAX_LINES),
                        markdown = isMarkdownFile(path) || type.substringBefore(';').equals("text/markdown", ignoreCase = true),
                        htmlInfo = loaded.takeIf { isHtmlFile(path) },
                    )
                    else -> FileState.Download(loaded)
                }
            }
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
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
                actions = {
                    FileDownloadAction(
                        available = info != null,
                        state = downloadState,
                        onDownload = { info?.let { downloads.start(sessionId, it.path, it.name, it.size) } },
                    )
                },
            )
        },
    ) { padding ->
        // The spinner fades into whatever the file turns out to be.
        AnimatedContent(
            targetState = state,
            contentKey = { it::class },
            transitionSpec = { fadeIn(tween(220)) togetherWith fadeOut(tween(150)) },
            modifier = Modifier.padding(padding.exceptBottom()).consumeWindowInsets(padding).fillMaxSize(),
            label = "file",
        ) { s ->
            val bottom = padding.calculateBottomPadding()
            Box(Modifier.fillMaxSize()) {
                when (s) {
                    FileState.Loading -> CircularProgressIndicator(Modifier.align(Alignment.Center))
                    is FileState.Error -> Text(s.message, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(16.dp))
                    is FileState.Image -> AsyncImage(model = s.bytes, contentDescription = path, modifier = Modifier.padding(bottom = bottom).fillMaxSize())
                    is FileState.Text -> TextFileView(sessionId, path, s.lines, s.markdown, s.htmlInfo, line, bottom)
                    is FileState.Media -> Box(Modifier.padding(bottom = bottom)) { MediaFileView(sessionId, s.info) { state = FileState.Download(s.info) } }
                    is FileState.Download -> Box(Modifier.padding(bottom = bottom)) { DownloadView(sessionId, s.info) }
                }
            }
        }
    }
}

@Composable
private fun FileDownloadAction(available: Boolean, state: DownloadState, onDownload: () -> Unit) {
    if (!available) return
    IconButton(onClick = onDownload, enabled = state !is DownloadState.Running) {
        when (state) {
            is DownloadState.Running -> CircularProgressIndicator(
                strokeWidth = 2.dp,
                modifier = Modifier.size(20.dp),
            )
            is DownloadState.Done -> Icon(Icons.Default.Check, contentDescription = "Saved to Downloads")
            else -> Icon(
                Icons.Default.Download,
                contentDescription = if (state is DownloadState.Failed) "Retry download" else "Download file",
            )
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

/** Returns whether a path is conventionally a Markdown document. */
internal fun isMarkdownFile(path: String): Boolean =
    path.substringAfterLast('.', "").lowercase() in MARKDOWN_EXTENSIONS

/** Returns whether a path is an HTML document that can be rendered by a browser. */
internal fun isHtmlFile(path: String): Boolean =
    path.substringAfterLast('.', "").lowercase() in HTML_EXTENSIONS

@Composable
private fun TextFileView(
    sessionId: String,
    path: String,
    lines: List<String>,
    markdown: Boolean,
    htmlInfo: FileInfoDto?,
    target: Int,
    bottom: Dp,
) {
    if (htmlInfo != null) {
        HtmlSourceView(sessionId, path, lines, target, bottom, htmlInfo)
        return
    }
    if (!markdown) {
        CodeView(path, lines, target, bottom)
        return
    }

    var rendered by rememberSaveable(sessionId, path) { mutableStateOf(false) }
    val source = remember(lines) { lines.joinToString("\n") }
    Column(Modifier.fillMaxSize()) {
        SingleChoiceSegmentedButtonRow(
            Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp),
        ) {
            SegmentedButton(
                selected = !rendered,
                onClick = { rendered = false },
                shape = SegmentedButtonDefaults.itemShape(0, 2),
            ) { Text("Source") }
            SegmentedButton(
                selected = rendered,
                onClick = { rendered = true },
                shape = SegmentedButtonDefaults.itemShape(1, 2),
            ) { Text("Markdown") }
        }
        Box(Modifier.weight(1f).fillMaxWidth()) {
            if (rendered) {
                MarkdownPreview(path, source, bottom)
            } else {
                CodeView(path, lines, target, bottom)
            }
        }
    }
}

/** Keeps HTML source inspection available while offering a rendered browser view. */
@Composable
private fun HtmlSourceView(
    sessionId: String,
    path: String,
    lines: List<String>,
    target: Int,
    bottom: Dp,
    info: FileInfoDto,
) {
    val context = LocalContext.current
    val downloads = context.container.downloads
    val downloadState by remember(sessionId, info.path) { downloads.state(sessionId, info.path) }.collectAsState()
    var openAfterDownload by remember(sessionId, info.path) { mutableStateOf(false) }
    var openFailed by remember(sessionId, info.path) { mutableStateOf(false) }

    LaunchedEffect(downloadState, openAfterDownload) {
        when (val state = downloadState) {
            is DownloadState.Done -> if (openAfterDownload) {
                openAfterDownload = false
                openFailed = !downloads.open(state)
            }
            is DownloadState.Failed -> openAfterDownload = false
            else -> Unit
        }
    }

    fun openInBrowser() {
        openFailed = false
        when (val state = downloadState) {
            is DownloadState.Done -> openFailed = !downloads.open(state)
            DownloadState.Idle, is DownloadState.Failed -> {
                openAfterDownload = true
                downloads.start(sessionId, info.path, info.name, info.size)
            }
            is DownloadState.Running -> openAfterDownload = true
        }
    }

    val error = when {
        openFailed -> "No app can open this file"
        downloadState is DownloadState.Failed -> (downloadState as DownloadState.Failed).message
        else -> null
    }
    Column(Modifier.fillMaxSize()) {
        Row(
            Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 8.dp),
            horizontalArrangement = Arrangement.End,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            when (val state = downloadState) {
                DownloadState.Idle, is DownloadState.Done, is DownloadState.Failed ->
                    Button(onClick = { openInBrowser() }) {
                        Text(if (state is DownloadState.Failed) "Retry in browser" else "Open in browser")
                    }
                is DownloadState.Running -> {
                    if (state.total > 0) {
                        val progress by animateFloatAsState(state.bytes.toFloat() / state.total, label = "htmlDownloadProgress")
                        LinearProgressIndicator(progress = { progress }, modifier = Modifier.width(180.dp))
                    } else {
                        LinearProgressIndicator(Modifier.width(180.dp))
                    }
                    Text("Preparing browser…", modifier = Modifier.padding(start = 12.dp))
                }
            }
        }
        ExpandingContent(value = error) {
            Text(it, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(horizontal = 16.dp))
        }
        Box(Modifier.weight(1f).fillMaxWidth()) {
            CodeView(path, lines, target, bottom)
        }
    }
}

@Composable
private fun MarkdownPreview(path: String, source: String, bottom: Dp) {
    val scrollState = rememberScrollState()
    SelectionContainer {
        Column(
            Modifier
                .fillMaxSize()
                .verticalScroll(scrollState)
                .padding(start = 16.dp, end = 16.dp, top = 8.dp, bottom = bottom + 12.dp),
        ) {
            Text(path, style = MaterialTheme.typography.labelSmall)
            MarkdownText(source, modifier = Modifier.fillMaxWidth().padding(top = 8.dp))
        }
    }
}

@Composable
private fun CodeView(path: String, lines: List<String>, target: Int, bottom: Dp) {
    val listState = rememberLazyListState()
    val language = remember(path) { languageForPath(path) }
    val source = remember(lines) { lines.joinToString("\n") { it.replace("\t", "    ") } }
    val highlightedLines = remember(source, language) { highlightCodeLines(source, language) }
    LaunchedEffect(target, lines.size) {
        if (target > 0) listState.scrollToItem((target - 1 - 5).coerceIn(0, (lines.size - 1).coerceAtLeast(0)))
    }
    val digits = lines.size.toString().length
    val highlight = MaterialTheme.colorScheme.tertiaryContainer
    Box(Modifier.fillMaxSize().horizontalScroll(rememberScrollState())) {
        SelectionContainer {
            LazyColumn(state = listState, modifier = Modifier.width(2000.dp), contentPadding = PaddingValues(bottom = bottom)) {
                item { Text(path, style = MaterialTheme.typography.labelSmall, modifier = Modifier.padding(8.dp)) }
                itemsIndexed(lines) { i, _ ->
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
                        HighlightedCodeLine(
                            tokens = highlightedLines.getOrElse(i) { listOf(CodeToken("")) },
                            style = MaterialTheme.typography.bodySmall,
                        )
                    }
                }
            }
        }
    }
}
