package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// trustPrefix starts the id of the interaction for a running session's
// folder-trust dialog. The dialog is read from the pane, not from the
// provider's history, so the gateway adds and answers it itself.
const trustPrefix = "trust:"

// withFolderTrust appends a pending folder-trust interaction when the
// session's agent waits at that dialog, as one resumed by Herdr after a
// restart does in a folder it was never trusted in. Nothing else may be
// pending: the dialog's wording could then be another prompt's context.
func (s *Server) withFolderTrust(ctx context.Context, sess model.Session, msgs []model.Message) []model.Message {
	if sess.PaneID == "" || sess.Status != model.StatusWaitingInput || len(pendingInteractions(msgs)) > 0 {
		return msgs
	}
	if !s.Launcher.PaneTrust(ctx, sess.Provider, sess.PaneID) {
		return msgs
	}
	ia := &model.Interaction{
		ID:        trustPrefix + sess.PaneID,
		Type:      model.InteractionApproval,
		Kind:      model.KindTrust,
		State:     model.InteractionPending,
		Title:     "Trust this folder?",
		Detail:    sess.Cwd,
		Supported: true,
		Decisions: []string{model.DecisionApprove, model.DecisionDeny},
	}
	// The session's time keeps the ETag stable while the dialog stays.
	return append(msgs, model.Message{
		ID: ia.ID, Role: model.RoleAssistant, Timestamp: sess.UpdatedAt,
		Blocks: []model.Block{{Type: model.BlockInteraction, Interaction: ia}},
	})
}

// respondTrust answers the dialog withFolderTrust showed for this pane.
func (s *Server) respondTrust(w http.ResponseWriter, r *http.Request, id, provider, pane string, req model.InteractionResponse) {
	accept := req.Decision == model.DecisionApprove
	if !accept && req.Decision != model.DecisionDeny {
		s.fail(w, r, id, "respond", badRequest("decision must be approve or deny"))
		return
	}
	if strings.TrimPrefix(req.InteractionID, trustPrefix) != pane {
		s.fail(w, r, id, "respond", providers.ErrInteractionGone)
		return
	}
	if err := s.Launcher.AnswerPaneTrust(r.Context(), provider, pane, accept); err != nil {
		s.fail(w, r, id, "respond", err)
		return
	}
	s.Sessions.Forget(id)
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}
