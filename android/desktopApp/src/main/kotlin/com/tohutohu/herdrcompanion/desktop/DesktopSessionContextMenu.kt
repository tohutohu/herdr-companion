package com.tohutohu.herdrcompanion.desktop

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.PointerEventType
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Text
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.PointerButton
import com.tohutohu.herdrcompanion.ui.sessions.SessionListItemUiState

/** Compose's internal ContextMenuArea is not public in the pinned 1.12.0 API. */
@OptIn(ExperimentalComposeUiApi::class)
@Composable
fun DesktopSessionContextMenu(
    item: SessionListItemUiState,
    onOpen: () -> Unit,
    onSplit: () -> Unit,
    onArchive: () -> Unit,
    onRefresh: () -> Unit,
    onCopyId: () -> Unit,
    onOpenDirectory: () -> Unit,
    content: @Composable () -> Unit,
) {
    var expanded by remember { mutableStateOf(false) }
    Box(
        Modifier
            .fillMaxWidth()
            .pointerInput(Unit) {
                awaitPointerEventScope {
                    while (true) {
                        val event = awaitPointerEvent(PointerEventPass.Initial)
                        if (event.type == PointerEventType.Press && event.button == PointerButton.Secondary) {
                            event.changes.forEach { it.consume() }
                            expanded = true
                        }
                    }
                }
            },
    ) {
        content()
        DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            DropdownMenuItem(
                text = { Text("Open") },
                onClick = { expanded = false; onOpen() },
            )
            DropdownMenuItem(
                text = { Text("Split") },
                onClick = { expanded = false; onSplit() },
            )
            DropdownMenuItem(
                text = { Text("Refresh") },
                onClick = { expanded = false; onRefresh() },
            )
            DropdownMenuItem(
                text = { Text(if (item.session.archived) "Unarchive" else "Archive") },
                onClick = { expanded = false; onArchive() },
            )
            DropdownMenuItem(
                text = { Text("Copy Session ID") },
                onClick = { expanded = false; onCopyId() },
            )
            if (!item.session.cwd.isNullOrBlank() || item.session.project.isNotBlank()) {
                DropdownMenuItem(
                    text = { Text("Open Project Directory") },
                    onClick = { expanded = false; onOpenDirectory() },
                )
            }
        }
    }
}
