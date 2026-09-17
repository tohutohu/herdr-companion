package com.tohutohu.herdrmobile.ui.sessions

import android.widget.Toast
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Archive
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Unarchive
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import com.tohutohu.herdrmobile.container
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch

/** What the archive / resume actions need to know about a session. */
data class SessionRef(val id: String, val live: Boolean, val archived: Boolean)

/**
 * Archive, unarchive and resume, shared by the list, archive and detail
 * screens. Call [Dialogs] once in the screen.
 */
class SessionActions internal constructor(
    private val scope: CoroutineScope,
    private val context: android.content.Context,
    private val onChanged: suspend () -> Unit,
) {
    /** Id of the session currently being changed, for progress UI. */
    var busyId by mutableStateOf<String?>(null)
        private set
    private var confirmArchive by mutableStateOf<SessionRef?>(null)

    private val repo get() = context.container.repository

    fun archive(s: SessionRef) {
        // Archiving a running session kills the agent: ask first.
        if (s.live) confirmArchive = s else run(s.id, "Archived") { repo.archive(s.id); null }
    }

    fun unarchive(s: SessionRef) = run(s.id, "Restored to the session list") { repo.unarchive(s.id); null }

    fun resume(s: SessionRef) = run(s.id, "Resumed in Herdr") { repo.resume(s.id) }

    private fun run(id: String, done: String, block: suspend () -> String?) {
        if (busyId != null) return
        busyId = id
        scope.launch {
            val msg = try {
                block() ?: done
            } catch (e: Exception) {
                "Failed: ${e.message}"
            } finally {
                busyId = null
            }
            runCatching { onChanged() }
            Toast.makeText(context, msg, Toast.LENGTH_LONG).show()
        }
    }

    @Composable
    fun Dialogs() {
        val s = confirmArchive ?: return
        AlertDialog(
            onDismissRequest = { confirmArchive = null },
            title = { Text("Stop and archive?") },
            text = { Text("The agent is running. Its Herdr pane will be closed. You can resume the session later.") },
            confirmButton = {
                TextButton(onClick = {
                    confirmArchive = null
                    run(s.id, "Stopped and archived") { repo.archive(s.id); null }
                }) { Text("Stop and archive") }
            },
            dismissButton = { TextButton(onClick = { confirmArchive = null }) { Text("Cancel") } },
        )
    }

    /** Menu entries that apply to [s]. */
    @Composable
    fun MenuItems(s: SessionRef, onDismiss: () -> Unit) {
        if (!s.live) {
            DropdownMenuItem(
                text = { Text("Resume in Herdr") },
                leadingIcon = { Icon(Icons.Default.PlayArrow, contentDescription = null) },
                onClick = {
                    onDismiss()
                    resume(s)
                },
            )
        }
        if (s.archived) {
            DropdownMenuItem(
                text = { Text("Unarchive") },
                leadingIcon = { Icon(Icons.Default.Unarchive, contentDescription = null) },
                onClick = {
                    onDismiss()
                    unarchive(s)
                },
            )
        } else {
            DropdownMenuItem(
                text = { Text(if (s.live) "Stop and archive" else "Archive") },
                leadingIcon = { Icon(Icons.Default.Archive, contentDescription = null) },
                onClick = {
                    onDismiss()
                    archive(s)
                },
            )
        }
    }

    @Composable
    fun Menu(s: SessionRef, expanded: Boolean, onDismiss: () -> Unit) {
        DropdownMenu(expanded = expanded, onDismissRequest = onDismiss) {
            MenuItems(s, onDismiss)
        }
    }
}

@Composable
fun rememberSessionActions(onChanged: suspend () -> Unit = {}): SessionActions {
    val scope = rememberCoroutineScope()
    val context = LocalContext.current.applicationContext
    return remember(scope, context) { SessionActions(scope, context, onChanged) }
}
