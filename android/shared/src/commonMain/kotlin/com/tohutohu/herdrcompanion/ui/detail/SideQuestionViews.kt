package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.tween
import androidx.compose.animation.core.RepeatMode
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.Orientation
import androidx.compose.foundation.gestures.draggable
import androidx.compose.foundation.gestures.rememberDraggableState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.Close
import androidx.compose.material3.Badge
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.IconButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.compositeOver
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isMetaPressed
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.input.pointer.PointerIcon
import androidx.compose.ui.input.pointer.pointerHoverIcon
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.tohutohu.herdrcompanion.ui.markdown.MarkdownText
import com.tohutohu.herdrcompanion.ui.markdown.proseStyle

/*
 * btw: questions asked on the side of a session. They get their own accent,
 * apart from the theme's, so the sheet never reads as the conversation.
 */
@Immutable
private data class SideColors(val accent: Color, val container: Color, val onContainer: Color)

@Composable
private fun sideColors(): SideColors {
    val scheme = MaterialTheme.colorScheme
    return if (scheme.background.luminance() < 0.5f) {
        // In the dark a saturated slab outshines the answers: the field only
        // leans toward the accent from the surfaces around it.
        val accent = Color(0xFFA9ABF5)
        SideColors(accent = accent, container = accent.copy(alpha = 0.12f).compositeOver(scheme.surfaceContainerHigh), onContainer = scheme.onSurface)
    } else {
        SideColors(accent = Color(0xFF5B5FC7), container = Color(0xFFECECFA), onContainer = Color(0xFF22246A))
    }
}

/** The composer's way into the sheet; it counts the questions kept while the sheet is shut. */
@Composable
internal fun SideQuestionButton(conversation: SideConversation, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val colors = sideColors()
    val count = conversation.entries.size
    Row(
        modifier
            .height(48.dp)
            .clip(CircleShape)
            .clickable(role = Role.Button, onClick = onClick)
            .padding(horizontal = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text("btw", color = colors.accent, fontWeight = FontWeight.ExtraBold, fontSize = 13.sp)
        if (count > 0 && !conversation.open) {
            Badge(
                containerColor = colors.accent,
                contentColor = MaterialTheme.colorScheme.surface,
                modifier = Modifier.padding(start = 3.dp),
            ) { Text("$count") }
        }
    }
}

/** Phone: the side conversation in a modal sheet over the session. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun SideQuestionSheet(
    sessionId: String,
    conversation: SideConversation,
    onAsk: (String) -> Unit,
    onRetry: (Long) -> Unit,
    onClear: () -> Unit,
    onClose: () -> Unit,
) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    ModalBottomSheet(
        onDismissRequest = onClose,
        sheetState = sheetState,
        containerColor = MaterialTheme.colorScheme.surface,
    ) {
        SideQuestionContent(
            sessionId = sessionId,
            conversation = conversation,
            onAsk = onAsk,
            onRetry = onRetry,
            onClear = onClear,
            onClose = onClose,
            modifier = Modifier.fillMaxHeight(0.62f).navigationBarsPadding().imePadding(),
        )
    }
}

/**
 * Desktop: the side conversation docked under the pane's conversation, which
 * keeps running above it. The grip at its top sets its height.
 */
@Composable
internal fun SideQuestionPane(
    sessionId: String,
    conversation: SideConversation,
    paneHeight: Dp,
    onAsk: (String) -> Unit,
    onRetry: (Long) -> Unit,
    onClear: () -> Unit,
    onClose: () -> Unit,
) {
    val density = LocalDensity.current
    var fraction by rememberSaveable(sessionId) { mutableFloatStateOf(0.46f) }
    val dragState = rememberDraggableState { delta ->
        val total = with(density) { paneHeight.toPx() }
        if (total > 0f) fraction = (fraction - delta / total).coerceIn(0.2f, 0.85f)
    }
    Surface(
        color = MaterialTheme.colorScheme.surfaceContainerLow,
        shape = RoundedCornerShape(topStart = 14.dp, topEnd = 14.dp),
        shadowElevation = 6.dp,
        modifier = Modifier.fillMaxWidth().height(paneHeight * fraction),
    ) {
        Column {
            Box(
                Modifier
                    .fillMaxWidth()
                    .height(14.dp)
                    .draggable(dragState, Orientation.Vertical)
                    .pointerHoverIcon(PointerIcon.Hand),
                contentAlignment = Alignment.Center,
            ) {
                Box(
                    Modifier
                        .size(width = 32.dp, height = 4.dp)
                        .clip(CircleShape)
                        .background(MaterialTheme.colorScheme.outlineVariant),
                )
            }
            SideQuestionContent(
                sessionId = sessionId,
                conversation = conversation,
                onAsk = onAsk,
                onRetry = onRetry,
                onClear = onClear,
                onClose = onClose,
                desktopKeys = true,
                modifier = Modifier.fillMaxHeight(),
            )
        }
    }
}

@Composable
private fun SideQuestionContent(
    sessionId: String,
    conversation: SideConversation,
    onAsk: (String) -> Unit,
    onRetry: (Long) -> Unit,
    onClear: () -> Unit,
    onClose: () -> Unit,
    modifier: Modifier = Modifier,
    desktopKeys: Boolean = false,
) {
    val colors = sideColors()
    val entries = conversation.entries
    val listState = rememberLazyListState()
    // A new question, or the answer to the last one, comes into view.
    val lastAnswered = entries.lastOrNull()?.pending == false
    LaunchedEffect(entries.size, lastAnswered) {
        if (entries.isNotEmpty()) listState.animateScrollToItem(entries.lastIndex)
    }
    Column(modifier) {
        Row(
            Modifier.fillMaxWidth().padding(start = 16.dp, end = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                "btw",
                color = colors.accent,
                fontWeight = FontWeight.Bold,
                style = MaterialTheme.typography.labelMedium,
                modifier = Modifier.clip(CircleShape).background(colors.container).padding(horizontal = 10.dp, vertical = 3.dp),
            )
            Text(
                "Not added to the conversation",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                modifier = Modifier.padding(start = 10.dp).weight(1f),
            )
            if (entries.isNotEmpty()) {
                TextButton(onClick = onClear) { Text("Clear", color = colors.accent, style = MaterialTheme.typography.labelMedium) }
            }
            IconButton(onClick = onClose) { Icon(Icons.Default.Close, contentDescription = "Close btw") }
        }
        Box(Modifier.weight(1f).fillMaxWidth()) {
            if (entries.isEmpty()) {
                Text(
                    "Ask about this session without stopping it. Claude answers from the conversation so far, without using tools.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(horizontal = 16.dp, vertical = 12.dp),
                )
            } else {
                SelectionContainer {
                    LazyColumn(
                        state = listState,
                        modifier = Modifier.fillMaxWidth(),
                        verticalArrangement = Arrangement.spacedBy(10.dp),
                    ) {
                        items(entries, key = { it.id }) { entry ->
                            SideQuestionItem(entry, isLast = entry.id == entries.last().id, onRetry = { onRetry(entry.id) })
                        }
                    }
                }
            }
        }
        SideQuestionInput(
            sessionId = sessionId,
            followUp = entries.isNotEmpty(),
            colors = colors,
            desktopKeys = desktopKeys,
            onAsk = onAsk,
            onClose = onClose,
        )
    }
}

@Composable
private fun SideQuestionItem(entry: SideQuestionEntry, isLast: Boolean, onRetry: () -> Unit) {
    Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Text(entry.question, style = proseStyle(), fontWeight = FontWeight.SemiBold)
        when {
            entry.answer != null -> MarkdownText(entry.answer, Modifier.fillMaxWidth())
            entry.error != null -> Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    entry.error,
                    color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.weight(1f, fill = false),
                )
                TextButton(onClick = onRetry) { Text("Retry") }
            }
            else -> ThinkingDots(Modifier.padding(vertical = 6.dp))
        }
        if (!isLast) HorizontalDivider(Modifier.padding(top = 6.dp), color = MaterialTheme.colorScheme.surfaceContainerHighest)
    }
}

@Composable
private fun ThinkingDots(modifier: Modifier = Modifier) {
    val accent = sideColors().accent
    val transition = rememberInfiniteTransition(label = "btwThinking")
    Row(modifier, horizontalArrangement = Arrangement.spacedBy(4.dp)) {
        repeat(3) { i ->
            val alpha by transition.animateFloat(
                initialValue = 0.25f,
                targetValue = 1f,
                animationSpec = infiniteRepeatable(tween(600, delayMillis = i * 200), RepeatMode.Reverse),
                label = "dot$i",
            )
            Box(Modifier.size(6.dp).alpha(alpha).clip(CircleShape).background(accent))
        }
    }
}

@Composable
private fun SideQuestionInput(
    sessionId: String,
    followUp: Boolean,
    colors: SideColors,
    desktopKeys: Boolean,
    onAsk: (String) -> Unit,
    onClose: () -> Unit,
) {
    var text by rememberSaveable(sessionId) { mutableStateOf("") }
    val focus = remember { FocusRequester() }
    // On a phone the keyboard would cover the answers, so it comes up only for a first question.
    LaunchedEffect(sessionId) { if (desktopKeys || !followUp) runCatching { focus.requestFocus() } }
    fun submit() {
        if (text.isBlank()) return
        onAsk(text)
        text = ""
    }
    val clear = Color.Transparent
    val sendAlpha by animateFloatAsState(if (text.isBlank()) 0.38f else 1f, label = "btwSend")
    Row(
        Modifier
            .fillMaxWidth()
            .padding(start = 12.dp, end = 12.dp, top = 6.dp, bottom = 10.dp)
            .clip(RoundedCornerShape(24.dp))
            .background(colors.container)
            .padding(start = 4.dp, end = 4.dp),
        verticalAlignment = Alignment.Bottom,
    ) {
        TextField(
            value = text,
            onValueChange = { text = it },
            placeholder = {
                Text(
                    if (followUp) "Ask more…" else "Ask on the side…",
                    style = MaterialTheme.typography.bodyMedium.copy(letterSpacing = 0.sp),
                )
            },
            textStyle = MaterialTheme.typography.bodyMedium.copy(letterSpacing = 0.sp, color = colors.onContainer),
            colors = TextFieldDefaults.colors(
                focusedContainerColor = clear,
                unfocusedContainerColor = clear,
                focusedIndicatorColor = clear,
                unfocusedIndicatorColor = clear,
            ),
            maxLines = 4,
            modifier = Modifier
                .weight(1f)
                .focusRequester(focus)
                .onPreviewKeyEvent { event ->
                    if (event.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                    when {
                        event.key == Key.Escape && desktopKeys -> {
                            onClose()
                            true
                        }
                        // Return asks (a desktop keyboard); Shift+Return breaks the line.
                        event.key == Key.Enter && desktopKeys && !event.isShiftPressed && !event.isMetaPressed && !event.isCtrlPressed -> {
                            submit()
                            true
                        }
                        else -> false
                    }
                },
        )
        IconButton(
            onClick = ::submit,
            enabled = text.isNotBlank(),
            colors = IconButtonDefaults.iconButtonColors(contentColor = colors.accent, disabledContentColor = colors.accent),
            modifier = Modifier.padding(bottom = 4.dp).alpha(sendAlpha),
        ) { Icon(Icons.AutoMirrored.Filled.Send, contentDescription = "Ask") }
    }
}
