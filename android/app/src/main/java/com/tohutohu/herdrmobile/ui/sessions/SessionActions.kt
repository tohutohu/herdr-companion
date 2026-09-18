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
import androidx.compose.material3.SnackbarDuration
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.SnackbarResult
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
 * Batches on different sessions may overlap (swiping several rows quickly).
 * Call [Dialogs] once in the screen.
 *
 * With a [snackbar] the result is shown there and archiving offline sessions
 * (or unarchiving) can be undone; without one it is a toast.
 */
class SessionActions internal constructor(
    private val scope: CoroutineScope,
    private val context: android.content.Context,
    private val snackbar: SnackbarHostState?,
    private val onChanged: suspend () -> Unit,
) {
    /** Sessions currently being changed, for progress UI. */
    var busyIds by mutableStateOf<Set<String>>(emptySet())
        private set
    private var confirmArchive by mutableStateOf<List<SessionRef>>(emptyList())

    private val repo get() = context.container.repository

    fun busy(id: String) = id in busyIds

    /** Whether [id] waits in the stop-and-archive confirmation. */
    fun pending(id: String) = confirmArchive.any { it.id == id }

    /** Busy or awaiting confirmation: a swiped row stays open while this holds. */
    fun engaged(id: String) = busy(id) || pending(id)

    fun archive(targets: List<SessionRef>) {
        if (targets.isEmpty()) return
        // Archiving a running session kills the agent: ask first.
        if (targets.any { it.live }) confirmArchive = targets else runArchive(targets)
    }

    fun archive(s: SessionRef) = archive(listOf(s))

    fun unarchive(targets: List<SessionRef>) =
        run(
            targets,
            done = "Restored to the session list",
            verb = "restored",
            undo = { archive(it.map { s -> s.copy(archived = false) }) },
        ) { repo.unarchive(it.id); null }

    fun unarchive(s: SessionRef) = unarchive(listOf(s))

    fun resume(s: SessionRef) = run(listOf(s), "Resumed in Herdr", "resumed") { repo.resume(it.id) }

    private fun runArchive(targets: List<SessionRef>) {
        val stopping = targets.any { it.live }
        run(
            targets,
            done = if (stopping) "Stopped and archived" else "Archived",
            verb = "archived",
            undo = { succeeded ->
                undoableTargets(succeeded, stopping)
                    .takeIf { it.isNotEmpty() }
                    ?.let { unarchive(it.map { s -> s.copy(archived = true) }) }
            },
            undoable = !stopping,
        ) { repo.archive(it.id); null }
    }

    /**
     * Runs [block] for each target not already busy, then reports once. When a
     * snackbar is available and [undoable], its action calls [undo] with the
     * sessions that succeeded.
     */
    private fun run(
        targets: List<SessionRef>,
        done: String,
        verb: String,
        undo: ((List<SessionRef>) -> Unit)? = null,
        undoable: Boolean = undo != null,
        block: suspend (SessionRef) -> String?,
    ) {
        val fresh = targets.filter { it.id !in busyIds }
        if (fresh.isEmpty()) return
        busyIds = busyIds + fresh.map { it.id }
        scope.launch {
            var warning: String? = null
            var failure: String? = null
            val succeeded = mutableListOf<SessionRef>()
            for (t in fresh) {
                try {
                    block(t)?.let { warning = it }
                    succeeded += t
                } catch (e: Exception) {
                    failure = e.message ?: e.toString()
                } finally {
                    busyIds = busyIds - t.id
                }
            }
            runCatching { onChanged() }
            val msg = BatchOutcome(fresh.size, fresh.size - succeeded.size, failure, warning).message(done, verb)
            val host = snackbar
            if (host == null) {
                Toast.makeText(context, msg, Toast.LENGTH_LONG).show()
                return@launch
            }
            val offerUndo = undo != null && undoable && succeeded.isNotEmpty()
            val result = host.showSnackbar(
                message = msg,
                actionLabel = if (offerUndo) "Undo" else null,
                // Leave time to notice a mistake; plain confirmations go quickly.
                duration = if (offerUndo) SnackbarDuration.Long else SnackbarDuration.Short,
            )
            if (result == SnackbarResult.ActionPerformed) undo?.invoke(succeeded)
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
fun rememberSessionActions(snackbar: SnackbarHostState? = null, onChanged: suspend () -> Unit = {}): SessionActions {
    val scope = rememberCoroutineScope()
    val context = LocalContext.current.applicationContext
    return remember(scope, context, snackbar) { SessionActions(scope, context, snackbar, onChanged) }
}
