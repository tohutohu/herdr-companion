package api

import (
	"errors"
	"net/http"

	"github.com/tohutohu/herdr-android-client/gateway/internal/agentupdate"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	if s.AgentUpdates == nil {
		writeError(w, http.StatusServiceUnavailable, "Agent updates are unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": s.AgentUpdates.List(r.Context())})
}
func (s *Server) updateAgent(w http.ResponseWriter, r *http.Request) {
	if s.AgentUpdates == nil {
		writeError(w, http.StatusServiceUnavailable, "Agent updates are unavailable")
		return
	}
	st, err := s.AgentUpdates.Start(r.PathValue("provider"))
	if errors.Is(err, agentupdate.ErrUnknown) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, st)
}
func (s *Server) terminalPane(r *http.Request) (string, error) {
	if pane := r.PathValue("pane"); pane != "" {
		if s.Launcher == nil {
			return "", providers.ErrNotFound
		}
		return s.Launcher.TerminalPane(r.Context(), pane)
	}
	return s.livePane(r.Context(), r.PathValue("id"))
}
func (s *Server) continueLaunch(w http.ResponseWriter, r *http.Request) {
	if s.Launcher == nil {
		writeError(w, http.StatusServiceUnavailable, "Session launching is unavailable")
		return
	}
	res, err := s.Launcher.Continue(r.Context(), r.PathValue("pane"))
	if err != nil {
		s.fail(w, r, "", "continue_launch", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
