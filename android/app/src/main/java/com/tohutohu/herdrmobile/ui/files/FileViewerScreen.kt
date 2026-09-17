package com.tohutohu.herdrmobile.ui.files

import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
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

private sealed interface FileState {
    data object Loading : FileState
    data class Text(val lines: List<String>) : FileState
    data class Image(val bytes: ByteArray) : FileState
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
            val (type, bytes) = api.fileContent(sessionId, path)
            when {
                type.startsWith("image/") -> FileState.Image(bytes)
                type.startsWith("text/") -> FileState.Text(bytes.decodeToString().lines().take(MAX_LINES))
                else -> FileState.Error("Cannot preview $type")
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
        Box(Modifier.padding(padding).fillMaxSize()) {
            when (val s = state) {
                FileState.Loading -> CircularProgressIndicator(Modifier.align(Alignment.Center))
                is FileState.Error -> Text(s.message, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(16.dp))
                is FileState.Image -> AsyncImage(model = s.bytes, contentDescription = path, modifier = Modifier.fillMaxSize())
                is FileState.Text -> CodeView(path, s.lines, line)
            }
        }
    }
}

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
