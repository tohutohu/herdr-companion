package com.tohutohu.herdrcompanion.data.api

import com.tohutohu.herdrcompanion.ui.detail.NotesUnsupportedException
import com.tohutohu.herdrcompanion.ui.detail.SessionNotesBackend

/**
 * [SessionNotesBackend] over [GatewayApi]. Listing answers 404 only from a
 * gateway without notes (an unknown session lists none), and removing a note
 * that is already gone is what was asked for.
 */
class GatewayNotesBackend(private val api: GatewayApi) : SessionNotesBackend {
    override suspend fun list(sessionId: String): List<NoteDto> = try {
        api.sessionNotes(sessionId)
    } catch (e: GatewayException) {
        if (e.code == 404) throw NotesUnsupportedException() else throw e
    }

    override suspend fun add(sessionId: String, text: String, expiresAt: String?): NoteDto =
        api.addNote(sessionId, text, expiresAt)

    override suspend fun update(noteId: String, text: String): NoteDto = api.updateNote(noteId, text)

    override suspend fun delete(noteId: String) {
        try {
            api.deleteNote(noteId)
        } catch (e: GatewayException) {
            if (e.code != 404) throw e
        }
    }
}
