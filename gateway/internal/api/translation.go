package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/translation"
)

type Translator interface {
	Session(model.Session) model.Session
	Status() translation.Status
	Start(string) error
	Stop() error
	Configure(bool, bool) error
	Input(context.Context, string, string) (string, error)
	Messages(string, []model.Message, bool) []model.Message
}

func (s *Server) translationStatus(w http.ResponseWriter, r *http.Request) {
	if s.Translation == nil {
		writeError(w, 503, "translation unavailable")
		return
	}
	writeJSON(w, 200, s.Translation.Status())
}
func (s *Server) configureTranslation(w http.ResponseWriter, r *http.Request) {
	if s.Translation == nil {
		writeError(w, 503, "translation unavailable")
		return
	}
	var req struct {
		Action string `json:"action"`
		Model  string `json:"model"`
		Input  bool   `json:"input"`
		Output bool   `json:"output"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req) != nil {
		writeError(w, 400, "invalid json")
		return
	}
	var err error
	switch req.Action {
	case "start":
		err = s.Translation.Start(req.Model)
	case "stop":
		err = s.Translation.Stop()
	case "configure":
		err = s.Translation.Configure(req.Input, req.Output)
	default:
		writeError(w, 400, "invalid translation action")
		return
	}
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 202, s.Translation.Status())
}

func (s *Server) translateInput(ctx context.Context, sessionID, text string) (string, error) {
	if s.Translation == nil {
		return text, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	return s.Translation.Input(ctx, sessionID, text)
}

func (s *Server) translatedSession(session model.Session) model.Session {
	if s.Translation != nil {
		return s.Translation.Session(session)
	}
	return session
}
func (s *Server) translatedSessions(list []model.Session) []model.Session {
	if s.Translation == nil {
		return list
	}
	out := append([]model.Session{}, list...)
	for i := range out {
		out[i] = s.Translation.Session(out[i])
	}
	return out
}
