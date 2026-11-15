package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/notes"
)

// noteRoutes serves the notes left on sessions (docs/api.md#メモ).
func (s *Server) noteRoutes(api *http.ServeMux) {
	if s.Notes == nil {
		return
	}
	api.HandleFunc("GET /v1/notes", s.listNotes)
	api.HandleFunc("GET /v1/notes/{note}", s.getNote)
	api.HandleFunc("PATCH /v1/notes/{note}", s.updateNote)
	api.HandleFunc("DELETE /v1/notes/{note}", s.deleteNote)
	api.HandleFunc("GET /v1/sessions/{id}/notes", s.sessionNotes)
	api.HandleFunc("POST /v1/sessions/{id}/notes", s.createNote)
	api.HandleFunc("DELETE /v1/sessions/{id}/notes", s.clearNotes)
}

// noteRequest is the body of a create or update. ExpiresAt is RFC 3339;
// on update null (or "") removes the expiry and leaving it out keeps it.
type noteRequest struct {
	Text      *string `json:"text"`
	Author    string  `json:"author,omitempty"`
	ExpiresAt *string `json:"expiresAt"`
	// hasExpiry tells an explicit null apart from a missing field.
	hasExpiry bool
}

func decodeNoteRequest(r *http.Request) (noteRequest, error) {
	var req noteRequest
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		return req, badRequest("invalid json")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil || json.Unmarshal(b, &req) != nil {
		return req, badRequest("invalid json")
	}
	_, req.hasExpiry = fields["expiresAt"]
	return req, nil
}

func (req noteRequest) change() (notes.Change, error) {
	c := notes.Change{Text: req.Text, Author: req.Author}
	if !req.hasExpiry {
		return c, nil
	}
	raw := req.ExpiresAt
	if raw == nil || *raw == "" {
		c.ExpiresAt = &time.Time{}
		return c, nil
	}
	t, err := time.Parse(time.RFC3339, *raw)
	if err != nil {
		return c, badRequest("expiresAt must be an RFC 3339 time or null")
	}
	c.ExpiresAt = &t
	return c, nil
}

func (s *Server) noteFail(w http.ResponseWriter, r *http.Request, sessionID, op string, err error) {
	switch {
	case errors.Is(err, notes.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, notes.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.fail(w, r, sessionID, op, err)
	}
}

// listNotes lists the notes of every session that is not archived; with
// archived=true those of archived sessions too.
func (s *Server) listNotes(w http.ResponseWriter, r *http.Request) {
	withArchived := r.URL.Query().Get("archived") == "true"
	out := []model.Note{}
	for _, n := range s.Notes.All() {
		if !withArchived && s.Archive != nil && s.Archive.Has(n.SessionID) {
			continue
		}
		out = append(out, n)
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": out})
}

func (s *Server) sessionNotes(w http.ResponseWriter, r *http.Request) {
	list := s.Notes.For(r.PathValue("id"))
	if list == nil {
		list = []model.Note{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": list})
}

func (s *Server) createNote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	req, err := decodeNoteRequest(r)
	if err != nil {
		s.fail(w, r, id, "create_note", err)
		return
	}
	c, err := req.change()
	if err != nil {
		s.fail(w, r, id, "create_note", err)
		return
	}
	// Only sessions the Gateway knows, archived ones included: a typo in an
	// id would otherwise keep a note nobody ever sees.
	if _, _, err := s.Sessions.Get(r.Context(), id); err != nil {
		s.fail(w, r, id, "create_note", err)
		return
	}
	n, err := s.Notes.Create(id, c)
	if err != nil {
		s.noteFail(w, r, id, "create_note", err)
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

func (s *Server) getNote(w http.ResponseWriter, r *http.Request) {
	n, err := s.Notes.Get(r.PathValue("note"))
	if err != nil {
		s.noteFail(w, r, "", "get_note", err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) updateNote(w http.ResponseWriter, r *http.Request) {
	req, err := decodeNoteRequest(r)
	if err != nil {
		s.fail(w, r, "", "update_note", err)
		return
	}
	c, err := req.change()
	if err != nil {
		s.fail(w, r, "", "update_note", err)
		return
	}
	if c.Text == nil && c.ExpiresAt == nil {
		s.fail(w, r, "", "update_note", badRequest("nothing to change: give text or expiresAt"))
		return
	}
	n, err := s.Notes.Update(r.PathValue("note"), c)
	if err != nil {
		s.noteFail(w, r, "", "update_note", err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) deleteNote(w http.ResponseWriter, r *http.Request) {
	n, err := s.Notes.Delete(r.PathValue("note"))
	if err != nil {
		s.noteFail(w, r, "", "delete_note", err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) clearNotes(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	gone, err := s.Notes.Clear(id)
	if err != nil {
		s.noteFail(w, r, id, "clear_notes", err)
		return
	}
	if gone == nil {
		gone = []model.Note{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": gone})
}

// withNotes puts the session's notes on it.
func (s *Server) withNotes(session model.Session) model.Session {
	if s.Notes != nil {
		session.Notes = s.Notes.For(session.ID)
	}
	return session
}
