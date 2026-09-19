package com.tohutohu.herdrmobile.ui.sessions

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Archive
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Unarchive
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable

/** Pure menu rendering; platform routes supply the action callbacks. */
@Composable
fun SessionActionMenuItems(
    s: SessionRef,
    onResume: () -> Unit,
    onUnarchive: () -> Unit,
    onArchive: () -> Unit,
    onDismiss: () -> Unit,
) {
    if (!s.live) {
        DropdownMenuItem(
            text = { Text("Resume in Herdr") },
            leadingIcon = { Icon(Icons.Default.PlayArrow, contentDescription = null) },
            onClick = { onDismiss(); onResume() },
        )
    }
    if (s.archived) {
        DropdownMenuItem(
            text = { Text("Unarchive") },
            leadingIcon = { Icon(Icons.Default.Unarchive, contentDescription = null) },
            onClick = { onDismiss(); onUnarchive() },
        )
    } else {
        DropdownMenuItem(
            text = { Text(if (s.live) "Stop and archive" else "Archive") },
            leadingIcon = { Icon(Icons.Default.Archive, contentDescription = null) },
            onClick = { onDismiss(); onArchive() },
        )
    }
}
