package com.tohutohu.herdrmobile.ui.sessions

import androidx.compose.foundation.layout.Box
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Archive
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.SelectAll
import androidx.compose.material.icons.filled.Unarchive
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue

/**
 * Which sessions a list screen has selected. Long pressing a row starts a
 * selection; while one is active, tapping a row toggles it.
 */
class SessionSelection {
    var ids by mutableStateOf<Set<String>>(emptySet())
        private set

    val active get() = ids.isNotEmpty()

    fun contains(id: String) = id in ids

    fun toggle(id: String) {
        ids = if (id in ids) ids - id else ids + id
    }

    fun selectAll(all: Collection<String>) {
        ids = all.toSet()
    }

    fun clear() {
        ids = emptySet()
    }

    /** Drops sessions that left the list while they were selected. */
    fun keepOnly(present: Collection<String>) {
        val kept = ids.intersect(present.toSet())
        if (kept.size != ids.size) ids = kept
    }
}

@Composable
fun rememberSessionSelection(): SessionSelection = remember { SessionSelection() }

/**
 * Replaces the screen's app bar while a selection is active. [all] is every
 * session on the screen, in display order, so "select all" and the batch
 * actions know what they act on.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SelectionTopBar(selection: SessionSelection, all: List<SessionRef>, actions: SessionActions) {
    val selected = all.filter { selection.contains(it.id) }
    val toArchive = selected.filter { !it.archived }
    val toUnarchive = selected.filter { it.archived }
    var menu by remember { mutableStateOf(false) }
    // Keep the last count while the bar fades out after the selection clears.
    var shownCount by remember { mutableIntStateOf(selected.size) }
    if (selected.isNotEmpty()) shownCount = selected.size
    TopAppBar(
        navigationIcon = {
            IconButton(onClick = { selection.clear() }) {
                Icon(Icons.Default.Close, contentDescription = "Leave selection")
            }
        },
        title = { Text("$shownCount selected") },
        actions = {
            if (toArchive.isNotEmpty()) {
                IconButton(onClick = {
                    selection.clear()
                    actions.archive(toArchive)
                }) { Icon(Icons.Default.Archive, contentDescription = "Archive") }
            }
            if (toUnarchive.isNotEmpty()) {
                IconButton(onClick = {
                    selection.clear()
                    actions.unarchive(toUnarchive)
                }) { Icon(Icons.Default.Unarchive, contentDescription = "Unarchive") }
            }
            Box {
                IconButton(onClick = { menu = true }) {
                    Icon(Icons.Default.MoreVert, contentDescription = "More actions")
                }
                DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                    // Resuming spawns a Herdr pane each, so keep it single.
                    selected.singleOrNull()?.takeIf { !it.live }?.let { s ->
                        DropdownMenuItem(
                            text = { Text("Resume in Herdr") },
                            leadingIcon = { Icon(Icons.Default.PlayArrow, contentDescription = null) },
                            onClick = {
                                menu = false
                                selection.clear()
                                actions.resume(s)
                            },
                        )
                    }
                    DropdownMenuItem(
                        text = { Text("Select all") },
                        leadingIcon = { Icon(Icons.Default.SelectAll, contentDescription = null) },
                        onClick = {
                            menu = false
                            selection.selectAll(all.map { it.id })
                        },
                    )
                }
            }
        },
        colors = TopAppBarDefaults.topAppBarColors(
            containerColor = MaterialTheme.colorScheme.secondaryContainer,
            titleContentColor = MaterialTheme.colorScheme.onSecondaryContainer,
            navigationIconContentColor = MaterialTheme.colorScheme.onSecondaryContainer,
            actionIconContentColor = MaterialTheme.colorScheme.onSecondaryContainer,
        ),
    )
}
