// Package api exposes the gateway HTTP API used by the Android app.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/files"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/sessions"
	"github.com/tohutohu/herdr-android-client/gateway/internal/uploads"
)

// Terminal is the Herdr subset used by the terminal fallback endpoints.
type Terminal interface {
	ReadPane(ctx context.Context, paneID string, lines int) (*herdr.ReadResult, error)
	SendKeys(ctx context.Context, paneID string, keys ...string) error
	SendText(ctx context.Context, paneID, text string) error
}

type Server struct {
	Sessions *sessions.Service
	Terminal Terminal
	Uploads  *uploads.Store
	Config   *config.Store
	Sink     deadletter.Sink
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	api := http.NewServeMux()
	api.HandleFunc("GET /v1/sessions", s.listSessions)
	api.HandleFunc("GET /v1/sessions/{id}", s.getSession)
	api.HandleFunc("GET /v1/sessions/{id}/messages", s.getMessages)
	api.HandleFunc("POST /v1/sessions/{id}/messages", s.postMessage)
	api.HandleFunc("POST /v1/sessions/{id}/respond", s.respond)
	api.HandleFunc("GET /v1/sessions/{id}/messages/{mid}/images/{n}", s.getImage)
	api.HandleFunc("GET /v1/sessions/{id}/files", s.listFiles)
	api.HandleFunc("GET /v1/sessions/{id}/files/content", s.fileContent)
	api.HandleFunc("GET /v1/sessions/{id}/terminal", s.readTerminal)
	api.HandleFunc("POST /v1/sessions/{id}/terminal", s.writeTerminal)
	api.HandleFunc("POST /v1/uploads", s.upload)
	api.HandleFunc("POST /v1/devices", s.registerDevice)
	api.HandleFunc("DELETE /v1/devices", s.unregisterDevice)
	mux.Handle("/v1/", s.auth(api))
	return logRequests(mux)
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := s.Config.Get().AuthToken
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || want == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		level := slog.LevelDebug
		if rec.status >= 500 {
			level = slog.LevelError
		} else if rec.status >= 400 {
			level = slog.LevelWarn
		}
		slog.Log(r.Context(), level, "http request", "operation", r.Method+" "+r.URL.Path, "status", rec.status, "duration_ms", time.Since(start).Milliseconds())
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// fail maps domain errors to HTTP statuses and logs them.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, sessionID, op string, err error) {
	status := http.StatusInternalServerError
	var herr *herdr.Error
	switch {
	case errors.Is(err, providers.ErrNotFound), errors.Is(err, files.ErrNotFound), errors.Is(err, uploads.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, providers.ErrNotLive), errors.Is(err, providers.ErrInteractionGone):
		status = http.StatusConflict
	case errors.Is(err, providers.ErrUnsupported):
		status = http.StatusUnprocessableEntity
	case errors.Is(err, files.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, files.ErrTooLarge), errors.Is(err, uploads.ErrTooLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, uploads.ErrUnsupportedType):
		status = http.StatusUnsupportedMediaType
	case errors.Is(err, files.ErrNoRoot):
		status = http.StatusConflict
	case errors.As(err, &herr):
		status = http.StatusBadGateway
		if herr.Code == "agent_blocked" {
			status = http.StatusConflict
		}
	case errors.Is(err, errBadRequest):
		status = http.StatusBadRequest
	}
	provider, _, _ := sessions.SplitID(sessionID)
	level := slog.LevelWarn
	if status >= 500 {
		level = slog.LevelError
	}
	slog.Log(r.Context(), level, "request failed", "provider", provider, "session_id", sessionID, "operation", op, "error", err)
	writeError(w, status, err.Error())
}

var errBadRequest = errors.New("bad request")

func badRequest(msg string) error { return errors.Join(errBadRequest, errors.New(msg)) }

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.Sessions.List(r.Context())
	if err != nil {
		s.fail(w, r, "", "list_sessions", err)
		return
	}
	if list == nil {
		list = []model.Session{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": list})
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, _, err := s.Sessions.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "get_session", err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// getMessages returns the session plus messages. With ?after=<id> only the
// messages from that id onward are returned (the anchor is included so that
// in-progress updates to it are seen). Unknown anchors return everything.
func (s *Server) getMessages(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, res, err := s.Sessions.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "get_messages", err)
		return
	}
	msgs, err := res.Provider.Messages(r.Context(), res.NativeID, res.Live)
	if err != nil {
		s.fail(w, r, id, "get_messages", err)
		return
	}
	msgs = messagesFrom(msgs, r.URL.Query().Get("after"))
	if msgs == nil {
		msgs = []model.Message{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": sess, "messages": msgs})
}

func messagesFrom(msgs []model.Message, after string) []model.Message {
	if after == "" {
		return msgs
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].ID == after {
			return msgs[i:]
		}
	}
	return msgs
}

type sendRequest struct {
	Text    string   `json:"text"`
	Uploads []string `json:"uploads"`
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req sendRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		s.fail(w, r, id, "send_message", badRequest("invalid json"))
		return
	}
	in := model.Input{Text: req.Text}
	for _, u := range req.Uploads {
		p, err := s.Uploads.Path(u)
		if err != nil {
			s.fail(w, r, id, "send_message", err)
			return
		}
		in.Images = append(in.Images, p)
	}
	if strings.TrimSpace(in.Text) == "" && len(in.Images) == 0 {
		s.fail(w, r, id, "send_message", badRequest("empty message"))
		return
	}
	res, err := s.Sessions.Resolve(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "send_message", err)
		return
	}
	if err := res.Provider.Send(r.Context(), res.NativeID, res.Live, in); err != nil {
		s.recordSendError(id, "send message: "+err.Error(), req, err)
		s.fail(w, r, id, "send_message", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) respond(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.InteractionResponse
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || req.InteractionID == "" {
		s.fail(w, r, id, "respond", badRequest("invalid json"))
		return
	}
	res, err := s.Sessions.Resolve(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "respond", err)
		return
	}
	if err := res.Provider.Respond(r.Context(), res.NativeID, res.Live, req); err != nil {
		kind := deadletter.SendError
		if errors.Is(err, providers.ErrUnsupported) {
			kind = deadletter.UnsupportedInteraction
		}
		if !errors.Is(err, providers.ErrInteractionGone) {
			provider, _, _ := sessions.SplitID(id)
			s.Sink.Record(provider, id, kind, "respond: "+err.Error(), req)
		}
		s.fail(w, r, id, "respond", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) recordSendError(id, msg string, payload any, err error) {
	if errors.Is(err, providers.ErrNotLive) {
		return
	}
	provider, _, _ := sessions.SplitID(id)
	s.Sink.Record(provider, id, deadletter.SendError, msg, payload)
}

func (s *Server) getImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		s.fail(w, r, id, "get_image", badRequest("invalid index"))
		return
	}
	res, err := s.Sessions.Resolve(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "get_image", err)
		return
	}
	mime, data, err := res.Provider.Image(r.Context(), res.NativeID, r.PathValue("mid"), n)
	if err != nil {
		s.fail(w, r, id, "get_image", err)
		return
	}
	if !strings.HasPrefix(mime, "image/") {
		mime = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Write(data)
}

// roots returns the directories a session may read: its workspace plus the
// upload directory (so sent images can be displayed).
func (s *Server) roots(ctx context.Context, id string) ([]string, error) {
	sess, _, err := s.Sessions.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if sess.Cwd == "" {
		return nil, files.ErrNoRoot
	}
	return []string{sess.Cwd, s.Uploads.Dir()}, nil
}

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	roots, err := s.roots(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "list_files", err)
		return
	}
	entries, err := files.List(roots, r.URL.Query().Get("path"))
	if err != nil {
		s.fileError(id, r.URL.Query().Get("path"), err)
		s.fail(w, r, id, "list_files", err)
		return
	}
	if entries == nil {
		entries = []files.Entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"root": roots[0], "entries": entries})
}

func (s *Server) fileContent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := r.URL.Query().Get("path")
	if p == "" {
		s.fail(w, r, id, "file_content", badRequest("path is required"))
		return
	}
	roots, err := s.roots(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "file_content", err)
		return
	}
	f, size, ctype, err := files.Open(roots, p)
	if err != nil {
		s.fileError(id, p, err)
		s.fail(w, r, id, "file_content", err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	io.Copy(w, f)
}

func (s *Server) fileError(id, path string, err error) {
	// Missing files are normal (stale references); record the rest.
	if errors.Is(err, files.ErrNotFound) {
		return
	}
	provider, _, _ := sessions.SplitID(id)
	s.Sink.Record(provider, id, deadletter.FileError, err.Error(), map[string]string{"path": path})
}

func (s *Server) livePane(ctx context.Context, id string) (string, error) {
	res, err := s.Sessions.Resolve(ctx, id)
	if err != nil {
		return "", err
	}
	if res.Live == nil {
		return "", providers.ErrNotLive
	}
	return res.Live.PaneID, nil
}

func (s *Server) readTerminal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	lines := 200
	if v, err := strconv.Atoi(r.URL.Query().Get("lines")); err == nil && v > 0 && v <= 2000 {
		lines = v
	}
	pane, err := s.livePane(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "read_terminal", err)
		return
	}
	out, err := s.Terminal.ReadPane(r.Context(), pane, lines)
	if err != nil {
		s.fail(w, r, id, "read_terminal", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"paneId": pane, "text": out.Text, "revision": out.Revision})
}

type terminalInput struct {
	Text string   `json:"text"`
	Keys []string `json:"keys"`
}

// Allowed keys for the terminal fallback; keeps the surface small.
var allowedKeys = map[string]bool{
	"enter": true, "esc": true, "tab": true, "shift+tab": true, "space": true, "backspace": true,
	"up": true, "down": true, "left": true, "right": true, "ctrl+c": true, "ctrl+d": true,
	"1": true, "2": true, "3": true, "4": true, "5": true, "6": true, "7": true, "8": true, "9": true,
	"y": true, "n": true,
}

func (s *Server) writeTerminal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req terminalInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		s.fail(w, r, id, "write_terminal", badRequest("invalid json"))
		return
	}
	for _, k := range req.Keys {
		if !allowedKeys[k] {
			s.fail(w, r, id, "write_terminal", badRequest("key not allowed: "+k))
			return
		}
	}
	pane, err := s.livePane(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "write_terminal", err)
		return
	}
	if req.Text != "" {
		if err := s.Terminal.SendText(r.Context(), pane, req.Text); err != nil {
			s.recordSendError(id, "terminal text: "+err.Error(), req, err)
			s.fail(w, r, id, "write_terminal", err)
			return
		}
	}
	if len(req.Keys) > 0 {
		if err := s.Terminal.SendKeys(r.Context(), pane, req.Keys...); err != nil {
			s.recordSendError(id, "terminal keys: "+err.Error(), req, err)
			s.fail(w, r, id, "write_terminal", err)
			return
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

// upload accepts the raw image body with its Content-Type.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, uploads.MaxUploadSize+1)
	id, err := s.Uploads.Save(r.Body, r.Header.Get("Content-Type"))
	if err != nil {
		s.fail(w, r, "", "upload", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

type deviceRequest struct {
	Name     string `json:"name"`
	FCMToken string `json:"fcmToken"`
}

func (s *Server) registerDevice(w http.ResponseWriter, r *http.Request) {
	var req deviceRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil || req.FCMToken == "" {
		s.fail(w, r, "", "register_device", badRequest("fcmToken is required"))
		return
	}
	if req.Name == "" {
		req.Name = "android"
	}
	if err := s.Config.UpsertDevice(req.Name, req.FCMToken); err != nil {
		s.fail(w, r, "", "register_device", err)
		return
	}
	slog.Info("device registered", "operation", "register_device", "device", req.Name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) unregisterDevice(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("fcmToken")
	if token == "" {
		s.fail(w, r, "", "unregister_device", badRequest("fcmToken is required"))
		return
	}
	if err := s.Config.RemoveDeviceToken(token); err != nil {
		s.fail(w, r, "", "unregister_device", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
