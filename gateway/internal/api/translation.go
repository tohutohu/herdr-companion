package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/translation"
)

type Translator interface {
	Session(model.Session, ...*bool) model.Session
	Status() translation.Status
	Start(string) error
	Stop() error
	Configure(bool, bool) error
	Input(context.Context, string, string, ...*bool) (string, error)
	RememberPrompt(string, string, string) error
	Messages(string, []model.Message, bool, ...*bool) []model.Message
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

func (s *Server) translateInput(ctx context.Context, sessionID, text string, files []string, mode ...*bool) (string, error) {
	if len(mode) == 0 {
		mode = []*bool{s.sessionTranslation(sessionID)}
	}
	if s.Translation == nil {
		if len(mode) > 0 && mode[0] != nil && *mode[0] {
			return "", errors.New("translation unavailable")
		}
		return text, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	translated, err := s.Translation.Input(ctx, sessionID, text, mode...)
	if err != nil {
		return "", err
	}
	if translated != text {
		sent := providers.TextWithFiles(model.Input{Text: translated, Files: files})
		if sent != translated {
			source := providers.TextWithFiles(model.Input{Text: text, Files: files})
			if err := s.Translation.RememberPrompt(sessionID, source, sent); err != nil {
				return "", err
			}
		}
	}
	return translated, nil
}

func (s *Server) translatedSession(session model.Session) model.Session {
	mode := s.sessionTranslation(session.ID)
	enabled := false
	if s.Translation != nil {
		session = s.Translation.Session(session, mode)
	}
	if mode != nil {
		enabled = *mode
	}
	session.Translation = &enabled
	return session
}
func (s *Server) translatedSessions(list []model.Session) []model.Session {
	out := append([]model.Session{}, list...)
	for i := range out {
		out[i] = s.translatedSession(out[i])
	}
	return out
}

func (s *Server) sessionTranslation(id string) *bool {
	if s.Direct != nil {
		return s.Direct.Translation(id)
	}
	return nil
}
