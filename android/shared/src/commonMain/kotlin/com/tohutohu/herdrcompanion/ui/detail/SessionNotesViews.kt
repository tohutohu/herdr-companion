package com.tohutohu.herdrcompanion.ui.detail

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import com.tohutohu.herdrcompanion.data.api.NoteDto
import kotlinx.coroutines.delay
import kotlin.time.Clock

/**
 * The notes left on a session for whoever watches it, with a field to add
 * one or rewrite one. Nothing here reaches the session's own agent.
 */
@Composable
internal fun SessionNotesDialog(sessionId: String, state: SessionNotesState, notes: SessionNotes) {
    val now by produceState(Clock.System.now()) {
        while (true) {
            delay(60_000)
            value = Clock.System.now()
        }
    }
    AlertDialog(
        onDismissRequest = { notes.setOpen(sessionId, false) },
        title = { Text("Notes") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(
                    "For the agents and people watching this session (hc note). The session's own agent does not see them.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                val list = state.notes
                when {
                    state.unsupported -> Text(
                        "This Gateway keeps no notes. Update the Gateway.",
                        color = MaterialTheme.colorScheme.error,
                        style = MaterialTheme.typography.bodySmall,
                    )
                    list == null && state.loading -> CircularProgressIndicator(Modifier.size(24.dp))
                    list.isNullOrEmpty() -> if (list != null) {
                        Text("No notes yet.", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    else -> LazyColumn(
                        verticalArrangement = Arrangement.spacedBy(6.dp),
                        modifier = Modifier.heightIn(max = 280.dp),
                    ) {
                        items(list, key = { it.id }) { note ->
                            NoteRow(
                                note = note,
                                details = noteDetails(note, now),
                                editing = state.editing == note.id,
                                onEdit = { notes.startEdit(sessionId, note.id) },
                                onDelete = { notes.delete(sessionId, note.id) },
                            )
                        }
                    }
                }
                if (!state.unsupported) {
                    OutlinedTextField(
                        value = state.draft,
                        onValueChange = { notes.setDraft(sessionId, it) },
                        label = { Text(if (state.editing != null) "Edit note" else "New note") },
                        placeholder = { Text("e.g. Have it fix review comments; archive once merged") },
                        enabled = !state.saving,
                        minLines = 2,
                        maxLines = 6,
                        modifier = Modifier.fillMaxWidth().testTag("noteDraft"),
                    )
                    // An edit keeps the note's expiry; only a new note picks one.
                    if (state.editing == null) {
                        Row(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
                            NoteExpiry.entries.forEach { expiry ->
                                FilterChip(
                                    selected = state.expiry == expiry,
                                    onClick = { notes.setExpiry(sessionId, expiry) },
                                    label = { Text(expiry.label) },
                                    enabled = !state.saving,
                                )
                            }
                        }
                    }
                }
                state.error?.let {
                    Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                }
            }
        },
        confirmButton = {
            if (!state.unsupported) {
                TextButton(onClick = { notes.save(sessionId) }, enabled = state.canSave) {
                    Text(if (state.editing != null) "Save" else "Add")
                }
            }
        },
        dismissButton = {
            if (state.editing != null) {
                TextButton(onClick = { notes.cancelEdit(sessionId) }) { Text("Cancel edit") }
            } else {
                TextButton(onClick = { notes.setOpen(sessionId, false) }) { Text("Close") }
            }
        },
    )
}

@Composable
private fun NoteRow(note: NoteDto, details: String, editing: Boolean, onEdit: () -> Unit, onDelete: () -> Unit) {
    Surface(
        color = if (editing) MaterialTheme.colorScheme.secondaryContainer else MaterialTheme.colorScheme.surfaceContainerHighest,
        shape = MaterialTheme.shapes.small,
        modifier = Modifier.fillMaxWidth(),
    ) {
        Row(Modifier.padding(start = 10.dp, top = 6.dp, bottom = 6.dp), verticalAlignment = Alignment.Top) {
            Column(Modifier.weight(1f).padding(top = 4.dp)) {
                SelectionContainer { Text(note.text, style = MaterialTheme.typography.bodyMedium) }
                if (details.isNotEmpty()) {
                    Text(details, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            IconButton(onClick = onEdit, modifier = Modifier.size(36.dp)) {
                Icon(Icons.Outlined.Edit, contentDescription = "Edit note", modifier = Modifier.size(18.dp))
            }
            IconButton(onClick = onDelete, modifier = Modifier.size(36.dp)) {
                Icon(Icons.Outlined.Delete, contentDescription = "Remove note", modifier = Modifier.size(18.dp))
            }
        }
    }
}

/** Shows the session's notes dialog while it is open, and reads the notes once the session is shown. */
@Composable
internal fun SessionNotesHost(sessionId: String, notes: SessionNotes, state: SessionNotesState) {
    LaunchedEffect(sessionId, notes) { notes.refresh(sessionId) }
    if (state.open) SessionNotesDialog(sessionId, state, notes)
}
