package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"slices"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/sessions"
)

// A reload finding names one way a question or approval was missed.
const (
	// findingNotShown: the app was not showing a pending interaction.
	findingNotShown = "not_shown"
	// findingStaleCache: a cached read lacked a pending interaction.
	findingStaleCache = "stale_cache"
	// findingUncaptured: the agent waits for an answer, but nothing pending
	// can be answered from the app.
	findingUncaptured = "uncaptured"
)

// screenLines is how much of the pane an uncaptured finding keeps.
const screenLines = 60

type reloadRequest struct {
	// Shown lists the pending interactions the app was showing. Omitted, the
	// app's side is not checked.
	Shown []string `json:"shown"`
}

type reloadFinding struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// reloadSession reads the conversation again without the provider's caches
// and checks that no question was missed on the way: by the app's polling,
// by the cached read, or by the provider itself. Each finding is recorded as
// a dead letter with what is needed to reproduce it.
func (s *Server) reloadSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req reloadRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err == nil && len(body) > 0 {
		err = json.Unmarshal(body, &req)
	}
	if err != nil {
		s.fail(w, r, id, "reload", badRequest("invalid json"))
		return
	}
	_, cached, err := s.Sessions.Conversation(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "reload", err)
		return
	}
	s.Sessions.Forget(id)
	sess, msgs, err := s.Sessions.Conversation(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "reload", err)
		return
	}
	if msgs == nil {
		msgs = []model.Message{}
	}

	pending := pendingInteractions(msgs)
	fresh := interactionIDs(pending)
	findings := []reloadFinding{}
	add := func(kind, msg string, raw map[string]any) {
		findings = append(findings, reloadFinding{Kind: kind, Message: msg})
		raw["finding"] = kind
		raw["status"] = sess.Status
		raw["pending"] = fresh
		provider, _, _ := sessions.SplitID(id)
		slog.Warn("reload found a missed interaction", "provider", provider, "session_id", id, "finding", kind)
		s.Sink.Record(provider, id, deadletter.MissedInteraction, msg, raw)
	}
	if missing := without(fresh, interactionIDs(pendingInteractions(cached))); len(missing) > 0 {
		add(findingStaleCache, "A cached read did not have a pending question", map[string]any{"missing": missing})
	}
	if req.Shown != nil {
		if missing := without(fresh, req.Shown); len(missing) > 0 {
			add(findingNotShown, "The app was not showing a pending question", map[string]any{"missing": missing, "shown": req.Shown})
		}
	}
	waiting := sess.Status == model.StatusWaitingInput || sess.Status == model.StatusWaitingApproval
	if waiting && !slices.ContainsFunc(pending, func(ia *model.Interaction) bool { return ia.Supported }) {
		raw := map[string]any{"paneId": sess.PaneID, "lastMessageIds": lastIDs(msgs, 3)}
		if sess.PaneID != "" {
			if out, err := s.Terminal.ReadPane(r.Context(), sess.PaneID, screenLines); err == nil {
				raw["screen"] = out.Text
			} else {
				raw["screenError"] = err.Error()
			}
		}
		add(findingUncaptured, "The agent is waiting for an answer the app cannot give", raw)
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": sess, "messages": msgs, "findings": findings})
}

func pendingInteractions(msgs []model.Message) []*model.Interaction {
	var out []*model.Interaction
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == model.BlockInteraction && b.Interaction != nil && b.Interaction.State == model.InteractionPending {
				out = append(out, b.Interaction)
			}
		}
	}
	return out
}

func interactionIDs(ias []*model.Interaction) []string {
	ids := []string{}
	for _, ia := range ias {
		ids = append(ids, ia.ID)
	}
	return ids
}

// without returns the ids in a that are not in b.
func without(a, b []string) []string {
	var out []string
	for _, id := range a {
		if !slices.Contains(b, id) {
			out = append(out, id)
		}
	}
	return out
}

func lastIDs(msgs []model.Message, n int) []string {
	ids := []string{}
	for _, m := range msgs[max(0, len(msgs)-n):] {
		ids = append(ids, m.ID)
	}
	return ids
}
