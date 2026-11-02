package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/files"
	"github.com/tohutohu/herdr-android-client/gateway/internal/forward"
	"github.com/tohutohu/herdr-android-client/gateway/internal/resident"
)

// ResidentAPIVersion is advertised in /v1/capabilities. Clients check it
// instead of reading a 404 as "no tasks".
const ResidentAPIVersion = 1

func (s *Server) residentRoutes(api *http.ServeMux) {
	api.HandleFunc("GET /v1/capabilities", s.capabilities)
	if s.Resident == nil {
		return
	}
	api.HandleFunc("GET /v1/resident/tasks", s.residentList)
	api.HandleFunc("POST /v1/resident/tasks", s.residentCreate)
	api.HandleFunc("POST /v1/resident/schedule/preview", s.residentPreview)
	api.HandleFunc("GET /v1/resident/tasks/{id}", s.residentGet)
	api.HandleFunc("PATCH /v1/resident/tasks/{id}", s.residentUpdate)
	api.HandleFunc("DELETE /v1/resident/tasks/{id}", s.residentDelete)
	api.HandleFunc("POST /v1/resident/tasks/{id}/pause", s.residentEnabled(false))
	api.HandleFunc("POST /v1/resident/tasks/{id}/resume", s.residentEnabled(true))
	api.HandleFunc("GET /v1/resident/tasks/{id}/messages", s.residentMessages)
	api.HandleFunc("POST /v1/resident/tasks/{id}/read", s.residentRead)
	api.HandleFunc("GET /v1/resident/tasks/{id}/runs", s.residentRuns)
	api.HandleFunc("POST /v1/resident/tasks/{id}/runs", s.residentRunNow)
	api.HandleFunc("GET /v1/resident/tasks/{id}/memory", s.residentMemory)
	api.HandleFunc("PUT /v1/resident/tasks/{id}/memory", s.residentPutMemory)
	api.HandleFunc("GET /v1/resident/tasks/{id}/artifacts", s.residentArtifacts)
	api.HandleFunc("GET /v1/resident/tasks/{id}/notifications", s.residentNotifications)
	api.HandleFunc("GET /v1/resident/notifications", s.residentAllNotifications)
	api.HandleFunc("GET /v1/resident/artifacts/{id}/content", s.residentArtifactContent)
	api.HandleFunc("GET /v1/resident/runs/{id}", s.residentRun)
	api.HandleFunc("POST /v1/resident/runs/{id}/cancel", s.residentCancel)
	api.HandleFunc("POST /v1/resident/runs/{id}/dismiss", s.residentDismiss)
	api.HandleFunc("POST /v1/resident/runs/{id}/promote", s.residentPromote)
	api.HandleFunc("POST /v1/resident/questions/{id}/answers", s.residentAnswer)
}

func (s *Server) capabilities(w http.ResponseWriter, r *http.Request) {
	caps := map[string]any{"messageIdempotency": s.MessageDelivery != nil}
	if proxy := forward.DirectAddress(s.Listen, r); proxy != "" {
		caps["localhostProxy"] = proxy
	}
	if s.Resident != nil {
		caps["resident"] = map[string]any{"version": ResidentAPIVersion, "providers": []string{resident.ProviderClaude, resident.ProviderCodex},
			"defaultTimezone": resident.DefaultTimezone}
	}
	writeJSON(w, http.StatusOK, caps)
}

// residentFail answers a resident error with a status and a code the apps
// switch on.
func (s *Server) residentFail(w http.ResponseWriter, r *http.Request, op string, err error) {
	status, code := http.StatusInternalServerError, "internal"
	switch {
	case errors.Is(err, resident.ErrNotFound), errors.Is(err, files.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, resident.ErrTrustRequired):
		status, code = http.StatusConflict, "trust_required"
	case errors.Is(err, resident.ErrConflict):
		status, code = http.StatusConflict, "conflict"
	case errors.Is(err, resident.ErrInvalid), errors.Is(err, errBadRequest):
		status, code = http.StatusBadRequest, "invalid"
	case errors.Is(err, files.ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, files.ErrTooLarge):
		status, code = http.StatusRequestEntityTooLarge, "too_large"
	}
	level := slog.LevelWarn
	if status >= 500 {
		level = slog.LevelError
	}
	slog.Log(r.Context(), level, "resident request failed", "operation", op, "status", status, "error", err)
	writeJSON(w, status, map[string]string{"error": err.Error(), "code": code})
}

func decodeBody(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return badRequest("invalid JSON body: " + err.Error())
	}
	return nil
}

func queryInt(r *http.Request, name string) int {
	n, _ := strconv.Atoi(r.URL.Query().Get(name))
	return n
}

func (s *Server) residentList(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.Resident.ListTasks(r.Context())
	if err != nil {
		s.residentFail(w, r, "resident.list", err)
		return
	}
	writeJSONWithETag(w, r, map[string]any{"tasks": tasks})
}

func (s *Server) residentCreate(w http.ResponseWriter, r *http.Request) {
	var req resident.CreateRequest
	if err := decodeBody(r, &req); err != nil {
		s.residentFail(w, r, "resident.create", err)
		return
	}
	t, err := s.Resident.CreateTask(r.Context(), req)
	if err != nil {
		s.residentFail(w, r, "resident.create", err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// TaskDetail is GET /v1/resident/tasks/{id}.
type TaskDetail struct {
	Task     *resident.Task     `json:"task"`
	Memory   *resident.Memory   `json:"memory"`
	Wakeups  []resident.Wakeup  `json:"wakeups"`
	Upcoming []time.Time        `json:"upcoming"`
	Recent   []resident.Run     `json:"recentRuns"`
	Pending  *resident.Question `json:"pendingQuestion,omitempty"`
}

func (s *Server) residentGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := s.Resident.GetTask(r.Context(), id)
	if err != nil {
		s.residentFail(w, r, "resident.get", err)
		return
	}
	mem, err := s.Resident.GetMemory(r.Context(), id)
	if err != nil {
		s.residentFail(w, r, "resident.get", err)
		return
	}
	wakes, err := s.Resident.Wakeups(r.Context(), id)
	if err != nil {
		s.residentFail(w, r, "resident.get", err)
		return
	}
	runs, _, err := s.Resident.Runs(r.Context(), id, "", 5)
	if err != nil {
		s.residentFail(w, r, "resident.get", err)
		return
	}
	var upcoming []time.Time
	if t.Enabled {
		upcoming, _, _ = s.Resident.PreviewSchedule(t.Schedule, 3)
	}
	if upcoming == nil {
		upcoming = []time.Time{}
	}
	writeJSONWithETag(w, r, TaskDetail{Task: t, Memory: mem, Wakeups: wakes, Upcoming: upcoming, Recent: runs, Pending: t.PendingQuestion})
}

func (s *Server) residentUpdate(w http.ResponseWriter, r *http.Request) {
	var req resident.UpdateRequest
	if err := decodeBody(r, &req); err != nil {
		s.residentFail(w, r, "resident.update", err)
		return
	}
	t, err := s.Resident.UpdateTask(r.Context(), r.PathValue("id"), req)
	if err != nil {
		s.residentFail(w, r, "resident.update", err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) residentDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Resident.DeleteTask(r.Context(), r.PathValue("id")); err != nil {
		s.residentFail(w, r, "resident.delete", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) residentEnabled(enabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Until *time.Time `json:"until,omitempty"`
		}
		if !enabled {
			if err := decodeBody(r, &req); err != nil {
				s.residentFail(w, r, "resident.enable", err)
				return
			}
		}
		t, err := s.Resident.SetEnabled(r.Context(), r.PathValue("id"), enabled, req.Until)
		if err != nil {
			s.residentFail(w, r, "resident.enable", err)
			return
		}
		writeJSON(w, http.StatusOK, t)
	}
}

func (s *Server) residentPreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Schedule resident.Schedule `json:"schedule"`
		Count    int               `json:"count"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.residentFail(w, r, "resident.preview", err)
		return
	}
	if req.Count <= 0 || req.Count > 10 {
		req.Count = 3
	}
	next, norm, err := s.Resident.PreviewSchedule(req.Schedule, req.Count)
	if err != nil {
		s.residentFail(w, r, "resident.preview", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"next": next, "schedule": norm})
}

func (s *Server) residentMessages(w http.ResponseWriter, r *http.Request) {
	msgs, more, err := s.Resident.Messages(r.Context(), r.PathValue("id"), r.URL.Query().Get("before"), queryInt(r, "limit"))
	if err != nil {
		s.residentFail(w, r, "resident.messages", err)
		return
	}
	writeJSONWithETag(w, r, map[string]any{"messages": msgs, "more": more})
}

type textRequest struct {
	Text      string `json:"text"`
	RequestID string `json:"requestId,omitempty"`
}

func (s *Server) residentRead(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MessageID string `json:"messageId,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.residentFail(w, r, "resident.read", err)
		return
	}
	if err := s.Resident.MarkRead(r.Context(), r.PathValue("id"), req.MessageID); err != nil {
		s.residentFail(w, r, "resident.read", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) residentRuns(w http.ResponseWriter, r *http.Request) {
	runs, more, err := s.Resident.Runs(r.Context(), r.PathValue("id"), r.URL.Query().Get("before"), queryInt(r, "limit"))
	if err != nil {
		s.residentFail(w, r, "resident.runs", err)
		return
	}
	writeJSONWithETag(w, r, map[string]any{"runs": runs, "more": more})
}

func (s *Server) residentRunNow(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RequestID string `json:"requestId,omitempty"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.residentFail(w, r, "resident.run_now", err)
		return
	}
	res, err := s.Resident.RunNow(r.Context(), r.PathValue("id"), req.RequestID)
	if err != nil {
		s.residentFail(w, r, "resident.run_now", err)
		return
	}
	writeJSON(w, http.StatusAccepted, res)
}

func (s *Server) residentMemory(w http.ResponseWriter, r *http.Request) {
	m, err := s.Resident.GetMemory(r.Context(), r.PathValue("id"))
	if err != nil {
		s.residentFail(w, r, "resident.memory", err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) residentPutMemory(w http.ResponseWriter, r *http.Request) {
	var req resident.MemoryUpdate
	if err := decodeBody(r, &req); err != nil {
		s.residentFail(w, r, "resident.memory", err)
		return
	}
	m, err := s.Resident.PutMemory(r.Context(), r.PathValue("id"), req)
	if err != nil {
		s.residentFail(w, r, "resident.memory", err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) residentArtifacts(w http.ResponseWriter, r *http.Request) {
	arts, err := s.Resident.Artifacts(r.Context(), r.PathValue("id"))
	if err != nil {
		s.residentFail(w, r, "resident.artifacts", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"artifacts": arts})
}

func (s *Server) residentNotifications(w http.ResponseWriter, r *http.Request) {
	ns, err := s.Resident.Notifications(r.Context(), r.PathValue("id"), queryInt(r, "limit"))
	if err != nil {
		s.residentFail(w, r, "resident.notifications", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": ns})
}

// residentAllNotifications lists the newest outbox entries of every task,
// for clients that show notifications themselves (the Mac UI polls it).
func (s *Server) residentAllNotifications(w http.ResponseWriter, r *http.Request) {
	ns, err := s.Resident.Notifications(r.Context(), "", queryInt(r, "limit"))
	if err != nil {
		s.residentFail(w, r, "resident.notifications", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": ns})
}

// residentArtifactContent serves an artifact's file. The stored path was
// checked when the run reported it and is checked again now, inside the
// task folder only, so a symlink changed since cannot lead elsewhere.
func (s *Server) residentArtifactContent(w http.ResponseWriter, r *http.Request) {
	a, cwd, err := s.Resident.Artifact(r.Context(), r.PathValue("id"))
	if err != nil {
		s.residentFail(w, r, "resident.artifact", err)
		return
	}
	download := r.URL.Query().Get("download") == "1"
	limit := int64(files.MaxFileSize)
	if download {
		limit = 0
	}
	f, size, ctype, err := files.Open([]string{cwd}, a.Path, limit)
	if err != nil {
		s.residentFail(w, r, "resident.artifact", err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if download {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(f.Name())}))
		var mod time.Time
		if st, err := f.Stat(); err == nil {
			mod = st.ModTime()
		}
		http.ServeContent(w, r, "", mod, f)
		return
	}
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	io.Copy(w, f)
}

func (s *Server) residentRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.Resident.GetRun(r.Context(), r.PathValue("id"))
	if err != nil {
		s.residentFail(w, r, "resident.run", err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) residentCancel(w http.ResponseWriter, r *http.Request) {
	run, err := s.Resident.CancelRun(r.Context(), r.PathValue("id"))
	if err != nil {
		s.residentFail(w, r, "resident.cancel", err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) residentDismiss(w http.ResponseWriter, r *http.Request) {
	run, err := s.Resident.DismissRun(r.Context(), r.PathValue("id"))
	if err != nil {
		s.residentFail(w, r, "resident.dismiss", err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// residentPromote makes a run's conversation an ordinary session and
// resumes it, so the user continues where the run left off. Promoting a run
// again opens the same session (resumed when it runs nowhere).
func (s *Server) residentPromote(w http.ResponseWriter, r *http.Request) {
	p, err := s.Resident.Promote(r.Context(), r.PathValue("id"))
	if err != nil {
		s.residentFail(w, r, "resident.promote", err)
		return
	}
	sess, res, err := s.Sessions.Get(r.Context(), p.SessionID)
	if err != nil {
		s.fail(w, r, p.SessionID, "resident.promote", err)
		return
	}
	if res.Live == nil || res.Live.Dormant {
		cwd := sess.Cwd
		if cwd == "" {
			cwd = p.Cwd
		}
		if _, err := s.Launcher.Resume(r.Context(), p.Provider, p.NativeID, cwd); err != nil {
			s.fail(w, r, p.SessionID, "resident.promote", err)
			return
		}
	}
	if err := s.Archive.Remove(p.SessionID); err != nil {
		slog.Warn("unarchive after promotion failed", "session_id", p.SessionID, "error", err)
	}
	writeJSON(w, http.StatusOK, map[string]string{"sessionId": p.SessionID})
}

func (s *Server) residentAnswer(w http.ResponseWriter, r *http.Request) {
	var req textRequest
	if err := decodeBody(r, &req); err != nil {
		s.residentFail(w, r, "resident.answer", err)
		return
	}
	res, err := s.Resident.AnswerQuestion(r.Context(), r.PathValue("id"), req.Text, req.RequestID)
	if err != nil {
		s.residentFail(w, r, "resident.answer", err)
		return
	}
	writeJSON(w, http.StatusAccepted, res)
}
