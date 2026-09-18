package com.tohutohu.herdrmobile.ui.detail

import android.text.format.Formatter
import androidx.compose.animation.Crossfade
import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.InsertDriveFile
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import com.tohutohu.herdrmobile.data.Message
import com.tohutohu.herdrmobile.data.api.BlockDto
import com.tohutohu.herdrmobile.data.api.InteractionResponseDto
import com.tohutohu.herdrmobile.ui.ExpandChevron
import com.tohutohu.herdrmobile.ui.markdown.MarkdownText

@Composable
fun MessageItem(
    message: Message,
    modifier: Modifier = Modifier,
    showRole: Boolean,
    providerName: String,
    resolveUrl: (String) -> String,
    onOpenFile: (String, Int) -> Unit,
    onOpenImage: (String) -> Unit,
    onOpenTerminal: () -> Unit,
    interactionsEnabled: Boolean,
    onRespond: (InteractionResponseDto) -> Unit,
) {
    val isUser = message.role == "user"
    Column(
        modifier
            .fillMaxWidth()
            .padding(horizontal = 12.dp, vertical = 2.dp),
        horizontalAlignment = if (isUser) Alignment.End else Alignment.Start,
    ) {
        if (showRole && message.role != "tool") {
            Text(
                when (message.role) {
                    "user" -> "You"
                    "assistant" -> providerName.substringBefore(' ')
                    else -> "System"
                },
                style = MaterialTheme.typography.labelMedium,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.primary,
                modifier = Modifier.padding(top = 12.dp, bottom = 2.dp),
            )
        }
        val content: @Composable () -> Unit = {
            Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                message.blocks.forEach { block ->
                    BlockView(message.role, block, resolveUrl, onOpenFile, onOpenImage, onOpenTerminal, interactionsEnabled, onRespond)
                }
            }
        }
        if (isUser) {
            Surface(
                color = MaterialTheme.colorScheme.primaryContainer,
                shape = RoundedCornerShape(12.dp),
                modifier = Modifier.widthIn(max = 320.dp),
            ) { Box(Modifier.padding(10.dp)) { content() } }
        } else {
            content()
        }
    }
}

@Composable
private fun BlockView(
    role: String,
    block: BlockDto,
    resolveUrl: (String) -> String,
    onOpenFile: (String, Int) -> Unit,
    onOpenImage: (String) -> Unit,
    onOpenTerminal: () -> Unit,
    interactionsEnabled: Boolean,
    onRespond: (InteractionResponseDto) -> Unit,
) {
    when (block.type) {
        "text" -> when (role) {
            "tool" -> CollapsibleTool(block.text.orEmpty(), output = true)
            "system" -> Text(
                block.text.orEmpty(),
                style = MaterialTheme.typography.bodySmall,
                fontStyle = FontStyle.Italic,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            else -> {
                val text = block.text.orEmpty()
                if (role == "assistant" && isToolCallText(text)) {
                    CollapsibleTool(text, output = false)
                } else {
                    SelectionContainer { MarkdownText(text) }
                }
            }
        }
        "image" -> block.url?.let { url ->
            AsyncImage(
                model = resolveUrl(url),
                contentDescription = "Image",
                contentScale = ContentScale.Fit,
                modifier = Modifier
                    .heightIn(max = 240.dp)
                    .clip(RoundedCornerShape(8.dp))
                    .clickable { onOpenImage(url) },
            )
        }
        "file" -> block.path?.let { path ->
            val context = LocalContext.current
            val label = buildString {
                // A file outside the workspace (e.g. a saved plan) is named by the gateway.
                append(block.text ?: path)
                if ((block.line ?: 0) > 0) append(":${block.line}")
                block.size?.let { append(" · ").append(Formatter.formatShortFileSize(context, it)) }
            }
            AssistChip(
                onClick = { onOpenFile(path, block.line ?: 0) },
                label = { Text(label, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.labelSmall) },
                leadingIcon = { Icon(Icons.AutoMirrored.Filled.InsertDriveFile, contentDescription = null) },
            )
        }
        "interaction" -> block.interaction?.let {
            InteractionCard(it, interactionsEnabled, onRespond, onOpenTerminal)
        }
        // Future block kinds degrade to their text.
        else -> Text(block.text ?: "Unsupported block: ${block.type}", style = MaterialTheme.typography.bodySmall)
    }
}

/** Tool calls and outputs start collapsed to one line; tap to toggle. */
@Composable
private fun CollapsibleTool(text: String, output: Boolean) {
    var expanded by rememberSaveable { mutableStateOf(false) }
    val collapsible = text.trimEnd().contains('\n') || text.length > 80
    Surface(
        color = if (output) MaterialTheme.colorScheme.surfaceVariant else MaterialTheme.colorScheme.surfaceContainerHigh,
        shape = RoundedCornerShape(6.dp),
        modifier = Modifier
            .fillMaxWidth()
            .clickable(enabled = collapsible) { expanded = !expanded }
            .animateContentSize(),
    ) {
        Row(Modifier.padding(horizontal = 8.dp, vertical = 6.dp), verticalAlignment = Alignment.Top) {
            // The surface animates its size; this only cross-fades the text.
            Crossfade(expanded, modifier = Modifier.weight(1f), label = "tool") { open ->
                if (open) {
                    SelectionContainer {
                        Text(
                            text.trimEnd(),
                            fontFamily = FontFamily.Monospace,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                } else {
                    Text(
                        toolSummary(text),
                        fontFamily = FontFamily.Monospace,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
            }
            if (collapsible) {
                ExpandChevron(
                    expanded = expanded,
                    contentDescription = if (expanded) "Collapse" else "Expand",
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(start = 4.dp).size(16.dp),
                )
            }
        }
    }
}
