package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

var errInvalidSetting = errors.New("invalid setting")

// validSetting checks a model, effort or mode against what the agent offers,
// so an unknown choice is refused with the reason instead of the agent
// quietly running something else. For an effort, model is the model it is
// meant for ("" for the catalog's general efforts).
func (s *Server) validSetting(ctx context.Context, provider, kind, value, model string) error {
	cat, err := s.Launcher.Models(ctx, provider)
	if err != nil {
		return err
	}
	var ids []string
	switch kind {
	case "model":
		if !providers.ValidModelID(value) {
			return fmt.Errorf("%w: invalid model id", errInvalidSetting)
		}
		for _, m := range cat.Models {
			ids = append(ids, m.ID)
		}
	case "effort":
		if !providers.ValidEffortID(value) {
			return fmt.Errorf("%w: invalid effort id", errInvalidSetting)
		}
		efforts := cat.Efforts
		for _, m := range cat.Models {
			if m.ID == model && len(m.Efforts) > 0 {
				efforts = m.Efforts
			}
		}
		for _, e := range efforts {
			ids = append(ids, e.ID)
		}
		if len(ids) == 0 {
			return fmt.Errorf("%w: this model offers no reasoning effort setting", errInvalidSetting)
		}
	case "mode":
		if !providers.ValidModeID(value) {
			return fmt.Errorf("%w: invalid mode id", errInvalidSetting)
		}
		for _, m := range cat.Modes {
			ids = append(ids, m.ID)
		}
	}
	for _, id := range ids {
		if id == value {
			return nil
		}
	}
	return fmt.Errorf("%w: %s %s is not available; choose one of: %s", errInvalidSetting, kind, value, strings.Join(ids, ", "))
}

type settingRequest struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

// liveSession resolves a session for a runtime operation; only a session the
// gateway runs has one (a stopped one is resumed by the runtime).
func (s *Server) liveSession(w http.ResponseWriter, r *http.Request, op string) (string, string, bool) {
	id := r.PathValue("id")
	res, err := s.Sessions.Resolve(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, op, err)
		return "", "", false
	}
	if res.Live == nil {
		s.fail(w, r, id, op, providers.ErrNotLive)
		return "", "", false
	}
	return id, res.Provider.Name(), true
}

func (s *Server) readSetting(w http.ResponseWriter, r *http.Request, id, op string) (settingRequest, bool) {
	var req settingRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		s.fail(w, r, id, op, badRequest("invalid json"))
		return req, false
	}
	return req, true
}

// setModel changes the model of a session, keeping its id and
// history. Claude applies it from the next model request, Codex from the
// next turn; the session reports it as pendingModel until then.
func (s *Server) setModel(w http.ResponseWriter, r *http.Request) {
	id, provider, ok := s.liveSession(w, r, "set_model")
	if !ok {
		return
	}
	req, ok := s.readSetting(w, r, id, "set_model")
	if !ok {
		return
	}
	if err := s.validSetting(r.Context(), provider, "model", req.Model, ""); err != nil {
		s.fail(w, r, id, "set_model", err)
		return
	}
	if err := s.Direct.SetModel(r.Context(), id, req.Model); err != nil {
		s.fail(w, r, id, "set_model", err)
		return
	}
	s.Sessions.Forget(id)
	s.writeSession(w, r, id)
}

// setEffort changes the reasoning effort; an effort the session's model
// does not offer is refused. The body may name the model the effort is for
// when both change together.
func (s *Server) setEffort(w http.ResponseWriter, r *http.Request) {
	id, provider, ok := s.liveSession(w, r, "set_effort")
	if !ok {
		return
	}
	req, ok := s.readSetting(w, r, id, "set_effort")
	if !ok {
		return
	}
	model := req.Model
	if model == "" {
		if sess, _, err := s.Sessions.Get(r.Context(), id); err == nil {
			model = sess.PendingModel
			if model == "" {
				model = sess.Model
			}
		}
	}
	if err := s.validSetting(r.Context(), provider, "effort", req.Effort, model); err != nil {
		s.fail(w, r, id, "set_effort", err)
		return
	}
	if err := s.Direct.SetEffort(r.Context(), id, req.Effort); err != nil {
		s.fail(w, r, id, "set_effort", err)
		return
	}
	s.Sessions.Forget(id)
	s.writeSession(w, r, id)
}

// interrupt stops the running turn of a session; the session stays.
func (s *Server) interrupt(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.liveSession(w, r, "interrupt")
	if !ok {
		return
	}
	if err := s.Direct.Interrupt(r.Context(), id); err != nil {
		s.fail(w, r, id, "interrupt", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

// Limits of a side question request.
const (
	maxSideQuestionBody    = 1 << 20
	maxSideQuestionHistory = 50
)

type sideQuestionRequest struct {
	Question string               `json:"question"`
	History  []providers.SideTurn `json:"history"`
}

// sideQuestion answers a question asked on the side of a session (Claude
// Code's /btw): from its conversation, without tools, while a turn may run.
// Nothing is added to the session's history; the caller keeps the earlier
// questions and answers and sends them back as history for a follow-up.
func (s *Server) sideQuestion(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.liveSession(w, r, "side_question")
	if !ok {
		return
	}
	var req sideQuestionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxSideQuestionBody)).Decode(&req); err != nil {
		s.fail(w, r, id, "side_question", badRequest("invalid json"))
		return
	}
	req.Question = strings.TrimSpace(req.Question)
	if req.Question == "" {
		s.fail(w, r, id, "side_question", badRequest("question is empty"))
		return
	}
	if len(req.History) > maxSideQuestionHistory {
		req.History = req.History[len(req.History)-maxSideQuestionHistory:]
	}
	answer, err := s.Direct.SideQuestion(r.Context(), id, req.Question, req.History)
	if err != nil {
		s.fail(w, r, id, "side_question", err)
		return
	}
	writeJSON(w, http.StatusOK, answer)
}

func (s *Server) writeSession(w http.ResponseWriter, r *http.Request, id string) {
	sess, _, err := s.Sessions.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
		return
	}
	writeJSON(w, http.StatusOK, s.translatedSession(sess))
}

// worktreeInUse reports whether a live session other than id works in path.
func (s *Server) worktreeInUse(ctx context.Context, id, path string) bool {
	live, err := s.Sessions.LiveSessions(ctx)
	if err != nil {
		return true
	}
	for _, sess := range live {
		if sess.ID == id || sess.Cwd == "" {
			continue
		}
		rel, err := filepath.Rel(path, sess.Cwd)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
