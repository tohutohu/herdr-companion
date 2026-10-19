package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.Crossfade
import androidx.compose.animation.animateContentSize
import androidx.compose.animation.core.tween
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.InsertDriveFile
import androidx.compose.material.icons.outlined.Info
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.layout.layout
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import com.tohutohu.herdrcompanion.data.Message
import com.tohutohu.herdrcompanion.data.api.BlockDto
import com.tohutohu.herdrcompanion.data.api.InteractionResponseDto
import com.tohutohu.herdrcompanion.ui.ExpandChevron
import com.tohutohu.herdrcompanion.ui.UiMotion
import com.tohutohu.herdrcompanion.ui.markdown.MarkdownText

/** A row of the message list, with how its turn ended when it is the last. */
@Composable
fun TranscriptRowItem(
    row: TranscriptRow,
    modifier: Modifier = Modifier,
    formatTime: (Long) -> String,
    resolveUrl: (String) -> String,
    formatFileSize: (Long) -> String,
    onOpenFile: (String, Int) -> Unit,
    onOpenImage: (String) -> Unit,
    onOpenTerminal: () -> Unit,
    interactionsEnabled: Boolean,
    onRespond: (InteractionResponseDto) -> Unit,
) {
    Column(modifier.fillMaxWidth()) {
        when (row) {
            is MessageRow -> MessageItem(
                message = row.message,
                resolveUrl = resolveUrl,
                formatFileSize = formatFileSize,
                onOpenFile = onOpenFile,
                onOpenImage = onOpenImage,
                onOpenTerminal = onOpenTerminal,
                interactionsEnabled = interactionsEnabled,
                onRespond = onRespond,
            )
            is ActivityRow -> ActivityGroupItem(
                row = row,
                resolveUrl = resolveUrl,
                formatFileSize = formatFileSize,
                onOpenFile = onOpenFile,
                onOpenImage = onOpenImage,
                onOpenTerminal = onOpenTerminal,
                interactionsEnabled = interactionsEnabled,
                onRespond = onRespond,
            )
            is WorkRow -> WorkGroupItem(row) { child ->
                TranscriptRowItem(
                    row = child,
                    formatTime = formatTime,
                    resolveUrl = resolveUrl,
                    formatFileSize = formatFileSize,
                    onOpenFile = onOpenFile,
                    onOpenImage = onOpenImage,
                    onOpenTerminal = onOpenTerminal,
                    interactionsEnabled = interactionsEnabled,
                    onRespond = onRespond,
                )
            }
        }
        row.end?.let { TurnEndLine(it, formatTime) }
    }
}

@Composable
fun MessageItem(
    message: Message,
    modifier: Modifier = Modifier,
    resolveUrl: (String) -> String,
    formatFileSize: (Long) -> String,
    onOpenFile: (String, Int) -> Unit,
    onOpenImage: (String) -> Unit,
    onOpenTerminal: () -> Unit,
    interactionsEnabled: Boolean,
    onRespond: (InteractionResponseDto) -> Unit,
) {
    val content: @Composable () -> Unit = {
        MessageBlocks(message, resolveUrl, formatFileSize, onOpenFile, onOpenImage, onOpenTerminal, interactionsEnabled, onRespond)
    }
    if (message.role == "user") {
        UserBubble(pending = false, delivery = if (message.queued) Delivery.Queued else null, modifier = modifier, content = content)
    } else {
        Column(modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 2.dp)) { content() }
    }
}

/**
 * The blocks of a message. One selection container goes around all of them
 * so the standard long-press toolbar can copy a range or select/copy every
 * text block in the message, including code and multi-paragraph text.
 */
@Composable
fun MessageBlocks(
    message: Message,
    resolveUrl: (String) -> String,
    formatFileSize: (Long) -> String,
    onOpenFile: (String, Int) -> Unit,
    onOpenImage: (String) -> Unit,
    onOpenTerminal: () -> Unit,
    interactionsEnabled: Boolean,
    onRespond: (InteractionResponseDto) -> Unit,
) {
    SelectionContainer {
        Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
            message.blocks.forEach { block ->
                BlockView(message.role, block, resolveUrl, formatFileSize, onOpenFile, onOpenImage, onOpenTerminal, interactionsEnabled, onRespond)
            }
        }
    }
}

/** How long a row takes to open or close. */
internal const val RESIZE_MS = UiMotion.ResizeMs

/**
 * Called just before a row opens or closes, so the list can move the rows
 * around it in step with it instead of animating them after it.
 */
internal val LocalRowResize = staticCompositionLocalOf<() -> Unit> { {} }

/** The reader's own messages: a plain neutral bubble, sent or on its way. */
internal val UserBubbleShape = RoundedCornerShape(20.dp)

/**
 * The work of a turn before its newest report, as one quiet line: how long it
 * took and how many tools it called. Like a tool line it opens upward, so the
 * line stays under the finger and the work unrolls toward the report.
 */
@Composable
fun WorkGroupItem(
    row: WorkRow,
    modifier: Modifier = Modifier,
    child: @Composable (TranscriptRow) -> Unit,
) {
    var expanded by rememberSaveable { mutableStateOf(false) }
    val onResize = LocalRowResize.current
    val expandable = row.rows.isNotEmpty()
    Column(modifier.fillMaxWidth()) {
        AnimatedVisibility(
            visible = expanded,
            enter = expandVertically(UiMotion.resize(), expandFrom = Alignment.Bottom) + fadeIn(UiMotion.resize()),
            exit = shrinkVertically(UiMotion.resize(), shrinkTowards = Alignment.Bottom) + fadeOut(tween(UiMotion.FadeOutMs)),
        ) {
            Column(Modifier.padding(vertical = 4.dp)) {
                row.rows.forEach { key(it.key) { child(it) } }
            }
        }
        val muted = MaterialTheme.colorScheme.onSurfaceVariant
        Row(
            Modifier
                .padding(horizontal = 12.dp, vertical = 2.dp)
                .clip(RoundedCornerShape(6.dp))
                .clickable(enabled = expandable) {
                    onResize()
                    expanded = !expanded
                }
                .padding(horizontal = 4.dp, vertical = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(workSummary(row), style = MaterialTheme.typography.labelSmall, color = muted, maxLines = 1)
            if (expandable) {
                ExpandChevron(
                    expanded = !expanded,
                    contentDescription = if (expanded) "Hide work" else "Show work",
                    tint = muted,
                    modifier = Modifier.padding(start = 2.dp).size(14.dp),
                )
            }
        }
    }
}

/** "Worked for 1m 26s · 4 tool calls", or "Working · 2 tool calls" until the turn is over. */
internal fun workSummary(row: WorkRow): String {
    val what = when {
        row.working -> "Working"
        row.durationMs != null -> "Worked for ${durationLabel(row.durationMs)}"
        else -> "Worked"
    }
    return when (val calls = row.callCount) {
        0 -> what
        1 -> "$what · 1 tool call"
        else -> "$what · $calls tool calls"
    }
}

/** Under a finished turn, small and quiet: "✓ Done · 19:37". */
@Composable
private fun TurnEndLine(end: TurnEnd, formatTime: (Long) -> String) {
    val label = if (end.failed) "✕ Failed" else "✓ Done"
    Text(
        listOfNotNull(label, end.at.takeIf { it > 0 }?.let(formatTime)).joinToString(" · "),
        style = MaterialTheme.typography.labelSmall,
        color = if (end.failed) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(start = 16.dp, end = 16.dp, top = 4.dp, bottom = 6.dp),
    )
}

/**
 * The tool calls between two reports, folded to the newest call as the CLI
 * shows it. Tapping opens every call and output, each still collapsible.
 */
@Composable
fun ActivityGroupItem(
    row: ActivityRow,
    modifier: Modifier = Modifier,
    resolveUrl: (String) -> String,
    formatFileSize: (Long) -> String,
    onOpenFile: (String, Int) -> Unit,
    onOpenImage: (String) -> Unit,
    onOpenTerminal: () -> Unit,
    interactionsEnabled: Boolean,
    onRespond: (InteractionResponseDto) -> Unit,
) {
    var expanded by rememberSaveable { mutableStateOf(false) }
    val onResize = LocalRowResize.current
    Column(
        modifier
            .fillMaxWidth()
            .padding(horizontal = 12.dp, vertical = 2.dp),
    ) {
        // The list is anchored at the bottom, so a row grows upward. The calls
        // open above the line, which stays under the finger, and unroll from
        // the newest one next to it.
        AnimatedVisibility(
            visible = expanded,
            enter = expandVertically(UiMotion.resize(), expandFrom = Alignment.Bottom) + fadeIn(UiMotion.resize()),
            exit = shrinkVertically(UiMotion.resize(), shrinkTowards = Alignment.Bottom) + fadeOut(tween(UiMotion.FadeOutMs)),
        ) {
            SelectionContainer {
                Column(verticalArrangement = Arrangement.spacedBy(4.dp), modifier = Modifier.padding(vertical = 4.dp)) {
                    row.parts.forEach { part ->
                        part.blocks.forEach { block ->
                            BlockView(part.role, block, resolveUrl, formatFileSize, onOpenFile, onOpenImage, onOpenTerminal, interactionsEnabled, onRespond)
                        }
                    }
                }
            }
        }
        val calls = row.callCount
        Row(
            Modifier
                .fillMaxWidth()
                .clip(RoundedCornerShape(6.dp))
                .clickable {
                    onResize()
                    expanded = !expanded
                }
                .padding(horizontal = 4.dp, vertical = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            val muted = MaterialTheme.colorScheme.onSurfaceVariant
            // While collapsed, each new call replaces the line.
            Crossfade(if (expanded) null else row.latest, modifier = Modifier.weight(1f), animationSpec = UiMotion.resize(), label = "activity") { latest ->
                Text(
                    latest?.let { "⋯ $it" } ?: "Hide",
                    fontFamily = FontFamily.Monospace,
                    style = MaterialTheme.typography.labelSmall,
                    color = muted,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            if (calls > 0) {
                Text(
                    if (calls == 1) "1 tool call" else "$calls tool calls",
                    style = MaterialTheme.typography.labelSmall,
                    color = muted,
                    maxLines = 1,
                    modifier = Modifier.padding(start = 8.dp),
                )
            }
            // The calls open upward, so the chevron points up until they do.
            ExpandChevron(
                expanded = !expanded,
                contentDescription = if (expanded) "Hide tool calls" else "Show tool calls",
                tint = muted,
                modifier = Modifier.padding(start = 4.dp).size(14.dp),
            )
        }
    }
}

/**
 * Caps a user bubble at 320dp on phones, and lets it grow to three quarters of
 * the row (up to 760dp) on wide windows so long messages don't wrap into a
 * narrow column.
 */
internal fun Modifier.userBubbleWidth(): Modifier = layout { measurable, constraints ->
    val cap = if (constraints.hasBoundedWidth) {
        (constraints.maxWidth * 0.75f).toInt().coerceIn(320.dp.roundToPx(), 760.dp.roundToPx())
    } else {
        320.dp.roundToPx()
    }
    val maxWidth = minOf(constraints.maxWidth, cap)
    val placeable = measurable.measure(
        constraints.copy(maxWidth = maxWidth, minWidth = minOf(constraints.minWidth, maxWidth)),
    )
    layout(placeable.width, placeable.height) { placeable.place(0, 0) }
}

@Composable
private fun BlockView(
    role: String,
    block: BlockDto,
    resolveUrl: (String) -> String,
    formatFileSize: (Long) -> String,
    onOpenFile: (String, Int) -> Unit,
    onOpenImage: (String) -> Unit,
    onOpenTerminal: () -> Unit,
    interactionsEnabled: Boolean,
    onRespond: (InteractionResponseDto) -> Unit,
) {
    when (block.type) {
        "text" -> when (role) {
            "tool" -> CollapsibleTool(block.text.orEmpty(), output = true)
            // A notice from the tool rather than the agent, marked by its icon.
            "system" -> Row {
                val muted = MaterialTheme.colorScheme.onSurfaceVariant
                Icon(
                    Icons.Outlined.Info,
                    contentDescription = "Notice",
                    tint = muted,
                    modifier = Modifier.padding(top = 2.dp, end = 6.dp).size(14.dp),
                )
                Text(block.text.orEmpty(), style = MaterialTheme.typography.bodySmall, color = muted)
            }
            else -> {
                val text = block.text.orEmpty()
                if (role == "assistant" && isToolCallText(text)) {
                    CollapsibleTool(text, output = false)
                } else {
                    MarkdownText(text)
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
            val label = buildString {
                // A file outside the workspace (e.g. a saved plan) is named by the gateway.
                append(block.text ?: path)
                if ((block.line ?: 0) > 0) append(":${block.line}")
                block.size?.let { append(" · ").append(formatFileSize(it)) }
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
    val onResize = LocalRowResize.current
    val collapsible = text.trimEnd().contains('\n') || text.length > 80
    Surface(
        color = if (output) MaterialTheme.colorScheme.surfaceVariant else MaterialTheme.colorScheme.surfaceContainerHigh,
        shape = RoundedCornerShape(6.dp),
        modifier = Modifier
            .fillMaxWidth()
            .clickable(enabled = collapsible) {
                onResize()
                expanded = !expanded
            }
            .animateContentSize(UiMotion.resize()),
    ) {
        Row(Modifier.padding(horizontal = 8.dp, vertical = 6.dp), verticalAlignment = Alignment.Top) {
            // The surface animates its size; this only cross-fades the text.
            Crossfade(expanded, modifier = Modifier.weight(1f), animationSpec = UiMotion.resize(), label = "tool") { open ->
                if (open) {
                    Text(
                        text.trimEnd(),
                        fontFamily = FontFamily.Monospace,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
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
