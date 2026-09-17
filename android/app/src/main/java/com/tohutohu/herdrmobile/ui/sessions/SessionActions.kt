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
 * screens. Every action takes a list so the lists can act on a multi-select;
 * the gateway has no batch endpoint, so they run one session at a time.
 * Call [Dialogs] once in the screen.
 */
class SessionActions internal constructor(
    private val scope: CoroutineScope,
    private val context: android.content.Context,
    private val onChanged: suspend () -> Unit,
) {
    /** Sessions currently being changed, for progress UI. */
    var busyIds by mutableStateOf<Set<String>>(emptySet())
        private set
    private var confirmArchive by mutableStateOf<List<SessionRef>>(emptyList())

    private val repo get() = context.container.repository

    fun busy(id: String) = id in busyIds

    fun archive(targets: List<SessionRef>) {
        if (targets.isEmpty()) return
        // Archiving a running session kills the agent: ask first.
        if (targets.any { it.live }) confirmArchive = targets else runArchive(targets)
    }

    fun archive(s: SessionRef) = archive(listOf(s))

    fun unarchive(targets: List<SessionRef>) =
        run(targets, "Restored to the session list", "restored") { repo.unarchive(it.id); null }

    fun unarchive(s: SessionRef) = unarchive(listOf(s))

    fun resume(s: SessionRef) = run(listOf(s), "Resumed in Herdr", "resumed") { repo.resume(it.id) }

    private fun runArchive(targets: List<SessionRef>) {
        val done = if (targets.any { it.live }) "Stopped and archived" else "Archived"
        run(targets, done, "archived") { repo.archive(it.id); null }
    }

    /** Runs [block] for each target, then reports once. */
    private fun run(targets: List<SessionRef>, done: String, verb: String, block: suspend (SessionRef) -> String?) {
        if (busyIds.isNotEmpty() || targets.isEmpty()) return
        busyIds = targets.map { it.id }.toSet()
        scope.launch {
            var warning: String? = null
            var failure: String? = null
            var failed = 0
            for (t in targets) {
                try {
                    block(t)?.let { warning = it }
                } catch (e: Exception) {
                    failed++
                    failure = e.message ?: e.toString()
                } finally {
                    busyIds = busyIds - t.id
                }
            }
            runCatching { onChanged() }
            val msg = when {
                targets.size == 1 -> failure?.let { "Failed: $it" } ?: warning ?: done
                failed == 0 -> "${targets.size} sessions $verb"
                failed == targets.size -> "Failed: $failure"
                else -> "${targets.size - failed} $verb, $failed failed: $failure"
            }
            Toast.makeText(context, msg, Toast.LENGTH_LONG).show()
        }
    }

    @Composable
    fun Dialogs() {
        val targets = confirmArchive
        if (targets.isEmpty()) return
        val live = targets.count { it.live }
        AlertDialog(
            onDismissRequest = { confirmArchive = emptyList() },
            title = { Text(if (targets.size == 1) "Stop and archive?" else "Stop and archive ${targets.size} sessions?") },
            text = {
                Text(
                    if (targets.size == 1) {
                        "The agent is running. Its Herdr pane will be closed. You can resume the session later."
                    } else {
                        "$live of them are running. Their Herdr panes will be closed. " +
                            "You can resume the sessions later."
                    },
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    confirmArchive = emptyList()
                    runArchive(targets)
                }) { Text("Stop and archive") }
            },
            dismissButton = { TextButton(onClick = { confirmArchive = emptyList() }) { Text("Cancel") } },
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
