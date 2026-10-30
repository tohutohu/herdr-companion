package api

import (
	"errors"
	"net/http"

	"github.com/tohutohu/herdr-android-client/gateway/internal/agentupdate"
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
