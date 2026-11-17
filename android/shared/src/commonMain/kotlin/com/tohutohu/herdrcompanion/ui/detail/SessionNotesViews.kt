package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.animation.animateContentSize
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.StickyNote2
import androidx.compose.material.icons.outlined.Schedule
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isMetaPressed
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.api.NoteDto
import kotlinx.coroutines.delay
import kotlin.time.Clock

/**
 * The session's notes, in small type just above the composer, so a glance at
 * the session shows what it is waiting for; and, while it is open, the field
 * to write one. Tapping a note shows when and by whom it was written and
 * offers to rewrite or remove it. Nothing here reaches the session's own agent.
 *
 * Reads the notes once the session is shown.
 */
@Composable
internal fun SessionNotesBar(sessionId: String, state: SessionNotesState, notes: SessionNotes, sendOnEnter: Boolean) {
    LaunchedEffect(sessionId, notes) { notes.refresh(sessionId) }
    val list = state.notes.orEmpty()
    if (list.isEmpty() && !state.open && state.error == null) return
    val now by produceState(Clock.System.now()) {
        while (true) {
            delay(60_000)
            value = Clock.System.now()
        }
    }
    var shown by remember(sessionId) { mutableStateOf<String?>(null) }
    Column(
        Modifier
            .fillMaxWidth()
            .animateContentSize()
            .padding(start = 16.dp, end = 16.dp, top = 4.dp, bottom = 2.dp)
            .testTag("sessionNotes"),
    ) {
        if (list.isNotEmpty()) {
            Column(Modifier.heightIn(max = 144.dp).verticalScroll(rememberScrollState())) {
                list.forEach { note ->
                    NoteLine(
                        note = note,
                        details = noteDetails(note, now),
                        expanded = shown == note.id,
                        editing = state.editing == note.id,
                        onToggle = { shown = if (shown == note.id) null else note.id },
                        onEdit = {
                            shown = null
                            notes.startEdit(sessionId, note.id)
                        },
                        onDelete = {
                            shown = null
                            notes.delete(sessionId, note.id)
                        },
                    )
                }
            }
        }
        if (state.open) {
            if (state.unsupported) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        "This Gateway keeps no notes. Update the Gateway.",
                        color = MaterialTheme.colorScheme.error,
                        style = MaterialTheme.typography.bodySmall,
                        modifier = Modifier.weight(1f),
                    )
                    TextButton(onClick = { notes.setOpen(sessionId, false) }) { Text("Close") }
                }
            } else {
                NoteField(sessionId, state, notes, sendOnEnter)
            }
        }
        state.error?.let {
            Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
        }
    }
}

@Composable
private fun NoteLine(
    note: NoteDto,
    details: String,
    expanded: Boolean,
    editing: Boolean,
    onToggle: () -> Unit,
    onEdit: () -> Unit,
    onDelete: () -> Unit,
) {
    val color = if (editing) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant
    Row(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(6.dp))
            .clickable(onClick = onToggle)
            .padding(vertical = 3.dp),
        verticalAlignment = Alignment.Top,
    ) {
        Icon(
            Icons.AutoMirrored.Outlined.StickyNote2,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.tertiary,
            modifier = Modifier.padding(top = 1.dp).size(14.dp),
        )
        Spacer(Modifier.width(6.dp))
        Column(Modifier.weight(1f)) {
            if (expanded) {
                SelectionContainer { Text(note.text, style = MaterialTheme.typography.bodySmall, color = color) }
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        details,
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.weight(1f),
                    )
                    SmallTextButton("Edit", onEdit)
                    SmallTextButton("Remove", onDelete)
                }
            } else {
                Text(
                    note.text,
                    style = MaterialTheme.typography.bodySmall,
                    color = color,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

/** Where a note is written: a slim field of its own above the composer, saved on its own. */
@Composable
private fun NoteField(sessionId: String, state: SessionNotesState, notes: SessionNotes, sendOnEnter: Boolean) {
    val focus = remember { FocusRequester() }
    LaunchedEffect(sessionId, state.editing) { runCatching { focus.requestFocus() } }
    val textStyle = MaterialTheme.typography.bodySmall.copy(color = MaterialTheme.colorScheme.onSurface)
    Column(
        Modifier
            .fillMaxWidth()
            .padding(top = 4.dp)
            .clip(RoundedCornerShape(12.dp))
            .background(MaterialTheme.colorScheme.tertiaryContainer.copy(alpha = 0.45f))
            .padding(start = 10.dp, end = 4.dp, top = 8.dp),
    ) {
        Row(verticalAlignment = Alignment.Top) {
            Icon(
                Icons.AutoMirrored.Outlined.StickyNote2,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.tertiary,
                modifier = Modifier.padding(top = 1.dp).size(14.dp),
            )
            Spacer(Modifier.width(6.dp))
            BasicTextField(
                value = state.draft,
                onValueChange = { notes.setDraft(sessionId, it) },
                enabled = !state.saving,
                textStyle = textStyle,
                cursorBrush = SolidColor(MaterialTheme.colorScheme.tertiary),
                maxLines = 5,
                modifier = Modifier
                    .weight(1f)
                    .padding(end = 6.dp)
                    .focusRequester(focus)
                    .testTag("noteDraft")
                    .onPreviewKeyEvent { event ->
                        if (event.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                        when {
                            event.key == Key.Escape -> {
                                notes.setOpen(sessionId, false)
                                true
                            }
                            event.key == Key.Enter && (event.isMetaPressed || event.isCtrlPressed || (sendOnEnter && !event.isShiftPressed)) -> {
                                notes.save(sessionId)
                                true
                            }
                            else -> false
                        }
                    },
                decorationBox = { inner ->
                    Box {
                        if (state.draft.isEmpty()) {
                            Text(
                                "Note on this session, for you and the agents watching it (not sent to its agent)",
                                style = textStyle,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                        inner()
                    }
                },
            )
        }
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(2.dp)) {
            // An edit keeps the note's expiry; only a new note picks one.
            if (state.editing == null) {
                val next = NoteExpiry.entries[(state.expiry.ordinal + 1) % NoteExpiry.entries.size]
                Row(
                    Modifier
                        .clip(RoundedCornerShape(6.dp))
                        .clickable(enabled = !state.saving) { notes.setExpiry(sessionId, next) }
                        .padding(horizontal = 4.dp, vertical = 6.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Icon(
                        Icons.Outlined.Schedule,
                        contentDescription = null,
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.size(14.dp),
                    )
                    Spacer(Modifier.width(4.dp))
                    Text(
                        if (state.expiry == NoteExpiry.NONE) "No expiry" else "Expires in ${state.expiry.label}",
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            } else {
                Text(
                    "Editing a note",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.padding(horizontal = 4.dp),
                )
            }
            Spacer(Modifier.weight(1f))
            if (state.saving) CircularProgressIndicator(Modifier.size(14.dp), strokeWidth = 2.dp)
            SmallTextButton("Cancel") { notes.setOpen(sessionId, false) }
            SmallTextButton(if (state.editing != null) "Save" else "Add note", enabled = state.canSave) { notes.save(sessionId) }
        }
    }
}

@Composable
private fun SmallTextButton(label: String, onClick: () -> Unit) = SmallTextButton(label, enabled = true, onClick = onClick)

@Composable
private fun SmallTextButton(label: String, enabled: Boolean, onClick: () -> Unit) {
    Text(
        label,
        style = MaterialTheme.typography.labelMedium,
        color = if (enabled) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.5f),
        modifier = Modifier
            .clip(RoundedCornerShape(6.dp))
            .clickable(enabled = enabled, role = Role.Button, onClick = onClick)
            .padding(horizontal = 8.dp, vertical = 6.dp),
    )
}
