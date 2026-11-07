package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.Message
import com.tohutohu.herdrcompanion.ui.markdown.MarkdownText
import kotlinx.coroutines.CancellationException

internal val LocalMessageTranslator = compositionLocalOf<(suspend (String) -> String)?> { null }

internal fun messageTranslationSource(message: Message): String =
    if (message.role != "assistant" || message.id.startsWith("streaming:")) ""
    else message.blocks.filter { it.type == "text" && !isToolCallText(it.originalText ?: it.text.orEmpty()) }
        .mapNotNull { (it.originalText ?: it.text)?.takeIf(String::isNotBlank) }.joinToString("\n\n")

/** Outside SelectionContainer: the text's long press and drag still belong to selection. */
@Composable
internal fun MessageTranslationMenu(message: Message) {
    val translate = LocalMessageTranslator.current ?: return
    val source = messageTranslationSource(message)
    if (source.isBlank()) return
    var menu by remember(message.id) { mutableStateOf(false) }
    var dialog by remember(message.id, source) { mutableStateOf(false) }
    var original by remember(message.id, source) { mutableStateOf(false) }
    var translated by remember(message.id, source) { mutableStateOf<String?>(null) }
    var error by remember(message.id, source) { mutableStateOf<String?>(null) }
    var attempt by remember(message.id, source) { mutableIntStateOf(0) }
    // Keep the visible icon small, with a gutter for its expanded touch target.
    // The target extends into the outer margin, rather than into selectable text.
    Box(Modifier.width(44.dp).padding(start = 12.dp)) {
        IconButton(onClick = { menu = true }, modifier = Modifier.size(32.dp)) {
            Icon(
                Icons.Default.MoreVert,
                contentDescription = "メッセージの操作",
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.size(16.dp),
            )
        }
        DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
            DropdownMenuItem(text = { Text("日本語に翻訳") }, onClick = {
                menu = false
                original = false
                dialog = true
            })
        }
    }
    if (dialog) {
        LaunchedEffect(source, attempt) {
            if (translated != null) return@LaunchedEffect
            error = null
            try {
                translated = translate(source)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                error = e.message ?: "翻訳に失敗しました"
            }
        }
        AlertDialog(
            onDismissRequest = { dialog = false },
            title = { Text(if (original) "原文" else "日本語訳") },
            text = {
                Column(Modifier.heightIn(max = 480.dp).verticalScroll(rememberScrollState())) {
                    when {
                        original -> SelectionContainer { MarkdownText(source) }
                        translated != null -> SelectionContainer { MarkdownText(translated!!) }
                        error != null -> Text(error!!, color = MaterialTheme.colorScheme.error)
                        else -> {
                            CircularProgressIndicator()
                            Text("翻訳中…")
                        }
                    }
                    if (error != null && !original) TextButton(onClick = { attempt++ }) { Text("再試行") }
                }
            },
            confirmButton = { TextButton(onClick = { dialog = false }) { Text("閉じる") } },
            dismissButton = {
                TextButton(onClick = { original = !original }) { Text(if (original) "日本語訳を表示" else "原文を表示") }
            },
        )
    }
}
