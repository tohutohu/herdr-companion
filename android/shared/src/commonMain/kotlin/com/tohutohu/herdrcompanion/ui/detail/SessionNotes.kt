package com.tohutohu.herdrcompanion.ui.detail

import com.tohutohu.herdrcompanion.data.api.NoteDto
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlin.time.Clock
import kotlin.time.Duration
import kotlin.time.Duration.Companion.days
import kotlin.time.Duration.Companion.minutes
import kotlin.time.Instant

/** Where the notes are kept: the gateway's `/v1/sessions/{id}/notes` and `/v1/notes/{id}`. */
interface SessionNotesBackend {
    /** Throws [NotesUnsupportedException] from a gateway that keeps no notes. */
    suspend fun list(sessionId: String): List<NoteDto>
    suspend fun add(sessionId: String, text: String, expiresAt: String?): NoteDto
    suspend fun update(noteId: String, text: String): NoteDto
    /** A note that is already gone counts as removed. */
    suspend fun delete(noteId: String)
}

class NotesUnsupportedException : Exception("This Gateway keeps no notes. Update the Gateway.")

/** How long a new note is kept. */
enum class NoteExpiry(val label: String, val duration: Duration?) {
    NONE("Keep", null),
    DAY("1 day", 1.days),
    WEEK("1 week", 7.days),
}

/**
 * One session's notes and the note being written. [notes] is null until
 * read; [editing] is the id of the note [draft] replaces, null for a new one.
 */
data class SessionNotesState(
    val notes: List<NoteDto>? = null,
    val loading: Boolean = false,
    val unsupported: Boolean = false,
    val error: String? = null,
    val open: Boolean = false,
    val draft: String = "",
    val editing: String? = null,
    val expiry: NoteExpiry = NoteExpiry.NONE,
    val saving: Boolean = false,
) {
    val canSave: Boolean get() = draft.isNotBlank() && !saving && !unsupported
}

/**
 * The notes left on sessions for whoever watches them (`hc note`): agents
 * that `hc wait` on a session, resident tasks, people. They are never sent to
 * the session's own agent. The gateway keeps them, so every device and agent
 * sees the same ones; this holds what was last read and the unsent draft.
 *
 * Calls run on [scope], so a save still lands after the session was left.
 */
class SessionNotes(
    private val scope: CoroutineScope,
    private val backend: SessionNotesBackend,
    private val now: () -> Instant = { Clock.System.now() },
) {
    private val state = MutableStateFlow<Map<String, SessionNotesState>>(emptyMap())

    val sessions: StateFlow<Map<String, SessionNotesState>> = state.asStateFlow()

    operator fun get(sessionId: String): SessionNotesState = state.value[sessionId] ?: SessionNotesState()

    /** Reads the session's notes again. */
    fun refresh(sessionId: String) {
        if (get(sessionId).loading) return
        edit(sessionId) { it.copy(loading = true) }
        scope.launch {
            val result = attempt { backend.list(sessionId) }
            edit(sessionId) { s ->
                result.fold(
                    onSuccess = { s.copy(notes = it, loading = false, unsupported = false, error = null) },
                    onFailure = { e ->
                        if (e is NotesUnsupportedException) {
                            s.copy(loading = false, unsupported = true, error = null)
                        } else {
                            s.copy(loading = false, error = e.reason("Could not read the notes."))
                        }
                    },
                )
            }
        }
    }

    /**
     * Opens or closes the field above the composer. Opening reads the notes
     * again; closing gives up an edit but keeps a new note's draft.
     */
    fun setOpen(sessionId: String, open: Boolean) {
        edit(sessionId) { s ->
            if (open) s.copy(open = true, error = null) else if (s.editing != null) s.copy(open = false, editing = null, draft = "") else s.copy(open = false)
        }
        if (open) refresh(sessionId)
    }

    fun setDraft(sessionId: String, text: String) = edit(sessionId) { it.copy(draft = text) }

    fun setExpiry(sessionId: String, expiry: NoteExpiry) = edit(sessionId) { it.copy(expiry = expiry) }

    /** Opens the field with a note's text in it, to be saved over it. */
    fun startEdit(sessionId: String, noteId: String) = edit(sessionId) { s ->
        val note = s.notes?.firstOrNull { it.id == noteId } ?: return@edit s
        s.copy(open = true, editing = note.id, draft = note.text, error = null)
    }

    fun cancelEdit(sessionId: String) = edit(sessionId) { it.copy(editing = null, draft = "") }

    /** Adds the draft as a note, or saves it over the note being edited, and closes the field. */
    fun save(sessionId: String) {
        val s = get(sessionId)
        if (!s.canSave) return
        val text = s.draft.trim()
        val editing = s.editing
        val expiresAt = s.expiry.duration?.let { Instant.fromEpochSeconds((now() + it).epochSeconds).toString() }
        edit(sessionId) { it.copy(saving = true, error = null) }
        scope.launch {
            val result = attempt { if (editing != null) backend.update(editing, text) else backend.add(sessionId, text, expiresAt) }
            edit(sessionId) { cur ->
                result.fold(
                    onSuccess = { note ->
                        val list = cur.notes.orEmpty()
                        val notes = if (list.any { it.id == note.id }) list.map { if (it.id == note.id) note else it } else list + note
                        cur.copy(notes = notes, saving = false, open = false, draft = "", editing = null, expiry = NoteExpiry.NONE)
                    },
                    onFailure = { cur.copy(saving = false, error = it.reason("Could not save the note.")) },
                )
            }
        }
    }

    fun delete(sessionId: String, noteId: String) {
        scope.launch {
            val result = attempt { backend.delete(noteId) }
            edit(sessionId) { cur ->
                result.fold(
                    onSuccess = {
                        val editing = cur.editing == noteId
                        cur.copy(
                            notes = cur.notes?.filterNot { it.id == noteId },
                            editing = if (editing) null else cur.editing,
                            draft = if (editing) "" else cur.draft,
                            error = null,
                        )
                    },
                    onFailure = { cur.copy(error = it.reason("Could not remove the note.")) },
                )
            }
        }
    }

    private suspend fun <T> attempt(block: suspend () -> T): Result<T> = try {
        Result.success(block())
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        Result.failure(e)
    }

    private fun Throwable.reason(fallback: String) = message?.takeIf { it.isNotBlank() } ?: fallback

    private fun edit(sessionId: String, change: (SessionNotesState) -> SessionNotesState) {
        state.update { all -> all + (sessionId to change(all[sessionId] ?: SessionNotesState())) }
    }
}

/**
 * "2h ago · by claude:5b27a61d · expires in 2d": when it was last written, by
 * whom, until when. A note the session left on itself is "by this session".
 */
fun noteDetails(note: NoteDto, now: Instant): String {
    val written = parseInstant(note.updatedAt) ?: parseInstant(note.createdAt)
    val by = (note.updatedBy ?: note.author)?.takeIf { it.isNotBlank() }?.let {
        if (it == note.sessionId) "this session" else shortSessionId(it)
    }
    val expires = note.expiresAt?.let(::parseInstant)
    return listOfNotNull(
        written?.let { if (now - it < 1.minutes) "just now" else "${span(now - it)} ago" },
        by?.let { "by $it" },
        expires?.let { if (it > now) "expires in ${span(it - now)}" else "expired" },
    ).joinToString(" · ")
}

private fun parseInstant(raw: String): Instant? = raw.takeIf { it.isNotBlank() }?.let { runCatching { Instant.parse(it) }.getOrNull() }

private fun span(d: Duration): String = when {
    d.inWholeDays >= 1 -> "${d.inWholeDays}d"
    d.inWholeHours >= 1 -> "${d.inWholeHours}h"
    else -> "${d.inWholeMinutes.coerceAtLeast(1)}m"
}

/** "claude:5b27a61d-…" → "claude:5b27a61d". */
private fun shortSessionId(id: String): String {
    val provider = id.substringBefore(':', "")
    val rest = id.substringAfter(':')
    return if (provider.isEmpty()) id.take(8) else "$provider:${rest.take(8)}"
}
