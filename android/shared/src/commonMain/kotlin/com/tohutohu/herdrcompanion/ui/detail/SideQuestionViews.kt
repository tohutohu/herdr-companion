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
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.selection.selectable
import androidx.compose.material3.ProvideTextStyle
import androidx.compose.runtime.key
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.text.style.TextOverflow
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

/** The composer's way back into the sheet; it counts the lanes kept while the sheet is shut. */
@Composable
internal fun SideQuestionButton(conversation: SideConversation, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val colors = sideColors()
    val count = conversation.lanes.size
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
internal fun SideQuestionSheet(sessionId: String, conversation: SideConversation, questions: SideQuestions) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    ModalBottomSheet(
        onDismissRequest = { questions.setOpen(sessionId, false) },
        sheetState = sheetState,
        containerColor = MaterialTheme.colorScheme.surface,
    ) {
        SideQuestionContent(
            sessionId = sessionId,
            conversation = conversation,
            questions = questions,
            modifier = Modifier.fillMaxHeight(0.62f).navigationBarsPadding().imePadding(),
        )
    }
}

/**
 * Desktop: the side conversation docked under the pane's conversation, which
 * keeps running above it. The grip at its top sets its height.
 */
@Composable
internal fun SideQuestionPane(sessionId: String, conversation: SideConversation, questions: SideQuestions, paneHeight: Dp) {
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
                questions = questions,
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
    questions: SideQuestions,
    modifier: Modifier = Modifier,
    desktopKeys: Boolean = false,
) {
    val colors = sideColors()
    val entries = conversation.entries
    val onClose = { questions.setOpen(sessionId, false) }
    // Each lane scrolls on its own.
    val listState = key(conversation.activeLane) { rememberLazyListState() }
    // A new question, or the answer to the last one, comes into view.
    val lastAnswered = entries.lastOrNull()?.pending == false
    LaunchedEffect(conversation.activeLane, entries.size, lastAnswered) {
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
            if (conversation.active != null) {
                TextButton(onClick = { questions.clear(sessionId) }) {
                    Text("Clear", color = colors.accent, style = MaterialTheme.typography.labelMedium)
                }
            }
            IconButton(onClick = onClose) { Icon(Icons.Default.Close, contentDescription = "Close btw") }
        }
        if (conversation.lanes.isNotEmpty()) {
            LaneChips(
                conversation = conversation,
                colors = colors,
                onSelect = { questions.selectLane(sessionId, it) },
                onNew = { questions.newLane(sessionId) },
            )
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
                            SideQuestionItem(entry, isLast = entry.id == entries.last().id, onRetry = { questions.retry(sessionId, entry.id) })
                        }
                    }
                }
            }
        }
        SideQuestionInput(
            sessionId = sessionId,
            lane = conversation.activeLane,
            followUp = entries.isNotEmpty(),
            colors = colors,
            desktopKeys = desktopKeys,
            onAsk = { questions.ask(sessionId, it) },
            onClose = onClose,
        )
    }
}

/** The lanes, named by their first question, and a way to start another. One still waiting shows dots. */
@Composable
private fun LaneChips(conversation: SideConversation, colors: SideColors, onSelect: (Long) -> Unit, onNew: () -> Unit) {
    val rowState = rememberLazyListState()
    // The lane shown, often one just started, comes into view.
    LaunchedEffect(conversation.lanes.size, conversation.activeLane) {
        val index = conversation.lanes.indexOfFirst { it.id == conversation.activeLane }
        rowState.animateScrollToItem(if (index >= 0) index else conversation.lanes.lastIndex)
    }
    // New stays in reach at the end however many lanes scroll beside it.
    Row(
        Modifier.fillMaxWidth().padding(start = 14.dp, end = 14.dp, bottom = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        LazyRow(
            state = rowState,
            horizontalArrangement = Arrangement.spacedBy(6.dp),
            modifier = Modifier.weight(1f, fill = false),
        ) {
            items(conversation.lanes, key = { it.id }) { lane ->
                LaneChip(selected = lane.id == conversation.activeLane, colors = colors, onClick = { onSelect(lane.id) }) {
                    Text(
                        lane.title,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.widthIn(max = 150.dp),
                    )
                    if (lane.pending) ThinkingDots(Modifier.padding(start = 6.dp), dot = 4.dp)
                }
            }
        }
        val fresh = conversation.activeLane == null
        LaneChip(selected = fresh, colors = colors, dashed = !fresh, onClick = onNew, modifier = Modifier.padding(start = 6.dp)) {
            Text(if (fresh) "New" else "+ New", color = if (fresh) Color.Unspecified else colors.accent)
        }
    }
}

@Composable
private fun LaneChip(
    selected: Boolean,
    colors: SideColors,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    dashed: Boolean = false,
    content: @Composable RowScope.() -> Unit,
) {
    val shape = CircleShape
    val outline = MaterialTheme.colorScheme.outlineVariant
    Row(
        modifier
            .height(32.dp)
            .clip(shape)
            .then(
                when {
                    selected -> Modifier.background(colors.container)
                    dashed -> Modifier.drawBehind {
                        drawRoundRect(
                            color = outline,
                            cornerRadius = CornerRadius(size.height / 2),
                            style = Stroke(width = 1.dp.toPx(), pathEffect = PathEffect.dashPathEffect(floatArrayOf(4.dp.toPx(), 3.dp.toPx()))),
                        )
                    }
                    else -> Modifier.border(1.dp, outline, shape)
                },
            )
            .selectable(selected = selected, role = Role.Tab, onClick = onClick)
            .padding(horizontal = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        ProvideTextStyle(
            MaterialTheme.typography.labelLarge.copy(
                letterSpacing = 0.sp,
                fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Normal,
                color = if (selected) colors.onContainer else MaterialTheme.colorScheme.onSurfaceVariant,
            ),
        ) { content() }
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
private fun ThinkingDots(modifier: Modifier = Modifier, dot: Dp = 6.dp) {
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
            Box(Modifier.size(dot).alpha(alpha).clip(CircleShape).background(accent))
        }
    }
}

@Composable
private fun SideQuestionInput(
    sessionId: String,
    lane: Long?,
    followUp: Boolean,
    colors: SideColors,
    desktopKeys: Boolean,
    onAsk: (String) -> Unit,
    onClose: () -> Unit,
) {
    var text by rememberSaveable(sessionId) { mutableStateOf("") }
    val focus = remember { FocusRequester() }
    // On a phone the keyboard would cover the answers, so it comes up only for a first question.
    LaunchedEffect(sessionId, lane) { if (desktopKeys || !followUp) runCatching { focus.requestFocus() } }
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
