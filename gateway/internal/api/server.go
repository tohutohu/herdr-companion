// Package api exposes the gateway HTTP API used by the Android app.
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/agentupdate"
	"github.com/tohutohu/herdr-android-client/gateway/internal/archive"
	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/directorycheck"
	"github.com/tohutohu/herdr-android-client/gateway/internal/files"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/launcher"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/sessions"
	"github.com/tohutohu/herdr-android-client/gateway/internal/uploads"
	"github.com/tohutohu/herdr-android-client/gateway/internal/usage"
	"github.com/tohutohu/herdr-android-client/gateway/internal/worktree"
)

// Terminal is the Herdr subset used by the terminal fallback endpoints.
type Terminal interface {
	ReadPane(ctx context.Context, paneID string, lines int) (*herdr.ReadResult, error)
	SendKeys(ctx context.Context, paneID string, keys ...string) error
	SendText(ctx context.Context, paneID, text string) error
}

type Server struct {
	pairing        pairingState
	AgentUpdates   *agentupdate.Service
	DirectoryCheck *directorycheck.Checker
	Sessions       *sessions.Service
	Terminal       Terminal
	Uploads        *uploads.Store
	Config         *config.Store
	Sink           deadletter.Sink
	Launcher       *launcher.Launcher
	Archive        *archive.Store
	// Usage is nil when subscription limit reporting is turned off.
	Usage *usage.Service
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /pair", s.redeemPairing)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	api := http.NewServeMux()
	api.HandleFunc("POST /v1/pairing", s.createPairing)
	api.HandleFunc("DELETE /v1/pairing", s.cancelPairing)
	api.HandleFunc("GET /v1/sessions", s.listSessions)
	api.HandleFunc("POST /v1/sessions", s.startSession)
	api.HandleFunc("POST /v1/sessions/archive", s.archiveSessions)
	api.HandleFunc("POST /v1/launches/{pane}/trust", s.answerTrust)
	api.HandleFunc("GET /v1/models", s.listModels)
	api.HandleFunc("GET /v1/agents", s.listAgents)
	api.HandleFunc("POST /v1/agents/{provider}/update", s.updateAgent)
	api.HandleFunc("GET /v1/launches/{pane}/terminal", s.readTerminal)
	api.HandleFunc("POST /v1/launches/{pane}/terminal", s.writeTerminal)
	api.HandleFunc("POST /v1/launches/{pane}/continue", s.continueLaunch)
	api.HandleFunc("GET /v1/directories", s.listDirectories)
	api.HandleFunc("POST /v1/directories", s.createDirectory)
	api.HandleFunc("POST /v1/directories/check", s.checkDirectory)
	api.HandleFunc("GET /v1/sessions/{id}", s.getSession)
	api.HandleFunc("POST /v1/sessions/{id}/archive", s.archiveSession)
	api.HandleFunc("DELETE /v1/sessions/{id}/archive", s.unarchiveSession)
	api.HandleFunc("POST /v1/sessions/{id}/resume", s.resumeSession)
	api.HandleFunc("GET /v1/sessions/{id}/messages", s.getMessages)
	api.HandleFunc("POST /v1/sessions/{id}/messages", s.postMessage)
	api.HandleFunc("POST /v1/sessions/{id}/respond", s.respond)
	api.HandleFunc("POST /v1/sessions/{id}/mode", s.cycleMode)
	api.HandleFunc("GET /v1/sessions/{id}/messages/{mid}/images/{n}", s.getImage)
	api.HandleFunc("GET /v1/sessions/{id}/files", s.listFiles)
	api.HandleFunc("GET /v1/sessions/{id}/files/content", s.fileContent)
	api.HandleFunc("GET /v1/sessions/{id}/files/stat", s.fileStat)
	api.HandleFunc("GET /v1/sessions/{id}/terminal", s.readTerminal)
	api.HandleFunc("POST /v1/sessions/{id}/terminal", s.writeTerminal)
	api.HandleFunc("POST /v1/uploads", s.upload)
	api.HandleFunc("GET /v1/usage", s.getUsage)
	api.HandleFunc("POST /v1/usage/refresh", s.refreshUsage)
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

// slowRequest is the duration from which a successful request is logged at
// info level, so slow responses show up in the log without debug logging.
const slowRequest = 500 * time.Millisecond

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		d := time.Since(start)
		slog.Log(r.Context(), requestLogLevel(rec.status, d), "http request", "operation", r.Method+" "+r.URL.Path, "status", rec.status, "duration_ms", d.Milliseconds())
	})
}

func requestLogLevel(status int, d time.Duration) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case status >= 400:
		return slog.LevelWarn
	case d >= slowRequest:
		return slog.LevelInfo
	}
	return slog.LevelDebug
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeJSONWithETag answers 304 Not Modified when the client already has
// this body, so polling clients skip receiving and applying it again.
func writeJSONWithETag(w http.ResponseWriter, r *http.Request, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sum := sha256.Sum256(buf.Bytes())
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(buf.Bytes())
}

func etagMatches(header, etag string) bool {
	for _, tag := range strings.Split(header, ",") {
		tag = strings.TrimPrefix(strings.TrimSpace(tag), "W/")
		if tag == etag || tag == "*" {
			return true
		}
	}
	return false
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// fail maps domain errors to HTTP statuses and logs them.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, sessionID, op string, err error) {
	status := http.StatusInternalServerError
	var herr *herdr.Error
	switch {
	case errors.Is(err, providers.ErrNotFound), errors.Is(err, files.ErrNotFound), errors.Is(err, uploads.ErrNotFound),
		errors.Is(err, launcher.ErrNoPendingTrust):
		status = http.StatusNotFound
	case errors.Is(err, providers.ErrNotLive), errors.Is(err, providers.ErrInteractionGone),
		errors.Is(err, errAlreadyLive), errors.Is(err, launcher.ErrNoCwd), errors.Is(err, launcher.ErrCwdGone),
		errors.Is(err, launcher.ErrStartRefused):
		status = http.StatusConflict
	case errors.Is(err, providers.ErrUnsupported):
		status = http.StatusUnprocessableEntity
	case errors.Is(err, files.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, files.ErrTooLarge), errors.Is(err, uploads.ErrTooLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, files.ErrNoRoot):
		status = http.StatusConflict
	case errors.As(err, &herr):
		status = http.StatusBadGateway
		if herr.Code == "agent_blocked" {
			status = http.StatusConflict
		}
	case errors.Is(err, errBadRequest), errors.Is(err, launcher.ErrInvalidName), errors.Is(err, launcher.ErrUnknownProvider), errors.Is(err, launcher.ErrInvalidModel),
		errors.Is(err, worktree.ErrNotRepository):
		status = http.StatusBadRequest
	case errors.Is(err, os.ErrExist):
		status = http.StatusConflict
	}
	provider, _, _ := sessions.SplitID(sessionID)
	level := slog.LevelWarn
	if status >= 500 {
		level = slog.LevelError
	}
	slog.Log(r.Context(), level, "request failed", "provider", provider, "session_id", sessionID, "operation", op, "error", err)
	writeError(w, status, err.Error())
}

var (
	errBadRequest  = errors.New("bad request")
	errAlreadyLive = errors.New("session is already running in herdr")
)

func badRequest(msg string) error { return errors.Join(errBadRequest, errors.New(msg)) }

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	var list []model.Session
	var err error
	if r.URL.Query().Get("archived") == "true" {
		list = s.Sessions.Archived(r.Context())
	} else {
		list, err = s.Sessions.List(r.Context())
	}
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
	if errors.Is(err, providers.ErrNotFound) && res.Live != nil {
		// A live session with no saved history yet has no messages.
		msgs, err = nil, nil
	}
	if err != nil {
		s.fail(w, r, id, "get_messages", err)
		return
	}
	msgs = messagesFrom(msgs, r.URL.Query().Get("after"))
	if msgs == nil {
		msgs = []model.Message{}
	}
	writeJSONWithETag(w, r, map[string]any{"session": sess, "messages": msgs})
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

// sendTimeout bounds a send that no longer has a client waiting on it. Claude
// needs a pause after each pasted image, so a message with several is slow.
const sendTimeout = 2 * time.Minute

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req sendRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		s.fail(w, r, id, "send_message", badRequest("invalid json"))
		return
	}
	in := model.Input{Text: req.Text}
	for _, u := range req.Uploads {
		f, err := s.Uploads.Get(u)
		if err != nil {
			s.fail(w, r, id, "send_message", err)
			return
		}
		if f.IsImage() {
			in.Images = append(in.Images, f.Path)
		} else {
			in.Files = append(in.Files, f.Path)
		}
	}
	if strings.TrimSpace(in.Text) == "" && len(in.Images) == 0 && len(in.Files) == 0 {
		s.fail(w, r, id, "send_message", badRequest("empty message"))
		return
	}
	res, err := s.Sessions.Resolve(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "send_message", err)
		return
	}
	// Once the message is here it goes to the agent even if the phone hangs up
	// (leaves the screen, loses the network): typing half a prompt into the
	// pane would be worse than finishing it.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), sendTimeout)
	defer cancel()
	if err := res.Provider.Send(ctx, res.NativeID, res.Live, in); err != nil {
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

// cycleMode delegates to the provider's own live-TUI mode switch. The
// provider decides which key or structured operation is safe for its agent.
func (s *Server) cycleMode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := s.Sessions.Resolve(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "cycle_mode", err)
		return
	}
	changer, ok := res.Provider.(providers.ModeChanger)
	if !ok {
		s.fail(w, r, id, "cycle_mode", providers.ErrUnsupported)
		return
	}
	if err := changer.CycleMode(r.Context(), res.NativeID, res.Live); err != nil {
		s.fail(w, r, id, "cycle_mode", err)
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

// roots returns the directories a session may read: its workspace, the
// upload directory (so sent images can be displayed) and any directory its
// provider links files from (Claude's saved plans).
func (s *Server) roots(ctx context.Context, id string) ([]string, error) {
	sess, res, err := s.Sessions.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if sess.Cwd == "" {
		return nil, files.ErrNoRoot
	}
	roots := []string{sess.Cwd, s.Uploads.Dir()}
	if fr, ok := res.Provider.(providers.FileRooter); ok {
		roots = append(roots, fr.FileRoots()...)
	}
	if fr, ok := res.Provider.(providers.SessionFileRooter); ok {
		roots = append(roots, fr.SessionFileRoots(ctx, res.NativeID)...)
	}
	return roots, nil
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
	download := r.URL.Query().Get("download") == "1"
	limit := int64(files.MaxFileSize)
	if download {
		limit = 0
	}
	f, size, ctype, err := files.Open(roots, p, limit)
	if err != nil {
		s.fileError(id, p, err)
		s.fail(w, r, id, "file_content", err)
		return
	}
	defer f.Close()
	if download {
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(f.Name())}))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		var mod time.Time
		if st, err := f.Stat(); err == nil {
			mod = st.ModTime()
		}
		// ServeContent handles Range so interrupted downloads can resume.
		http.ServeContent(w, r, "", mod, f)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	io.Copy(w, f)
}

func (s *Server) fileStat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p := r.URL.Query().Get("path")
	if p == "" {
		s.fail(w, r, id, "file_stat", badRequest("path is required"))
		return
	}
	roots, err := s.roots(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "file_stat", err)
		return
	}
	info, err := files.Stat(roots, p)
	if err != nil {
		s.fileError(id, p, err)
		s.fail(w, r, id, "file_stat", err)
		return
	}
	writeJSON(w, http.StatusOK, info)
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
	pane, err := s.terminalPane(r)
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
	pane, err := s.terminalPane(r)
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

// upload accepts the raw file body with its Content-Type. The client may name
// the file with a Content-Disposition header; without one the name is derived
// from the content type.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, uploads.MaxUploadSize+1)
	f, err := s.Uploads.Save(r.Body, r.Header.Get("Content-Type"), uploadName(r))
	if err != nil {
		s.fail(w, r, "", "upload", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": f.ID, "name": f.Name})
}

func uploadName(r *http.Request) string {
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Disposition"))
	if err != nil {
		return ""
	}
	return params["filename"]
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

func (s *Server) startSession(w http.ResponseWriter, r *http.Request) {
	var req launcher.StartRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || req.Provider == "" || req.Cwd == "" {
		s.fail(w, r, "", "start_session", badRequest("provider and cwd are required"))
		return
	}
	res, err := s.Launcher.Start(r.Context(), req)
	if err != nil {
		s.Sink.Record(req.Provider, "", deadletter.SendError, "start session: "+err.Error(), req)
		s.fail(w, r, "", "start_session", err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

// answerTrust continues (or, when declined, cancels) a new session that
// stopped at the agent's folder-trust dialog.
func (s *Server) answerTrust(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Trust bool `json:"trust"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		s.fail(w, r, "", "answer_trust", badRequest("invalid json"))
		return
	}
	res, err := s.Launcher.AnswerTrust(r.Context(), r.PathValue("pane"), req.Trust)
	if err != nil {
		s.fail(w, r, "", "answer_trust", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	cat, err := s.Launcher.Models(r.Context(), provider)
	if err != nil {
		s.fail(w, r, "", "list_models", err)
		return
	}
	if cat.Models == nil {
		cat.Models = []providers.ModelOption{}
	}
	writeJSON(w, http.StatusOK, cat)
}

func (s *Server) listDirectories(w http.ResponseWriter, r *http.Request) {
	res, err := s.Launcher.List(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, r, "", "list_directories", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

type mkdirRequest struct {
	Parent string `json:"parent"`
	Name   string `json:"name"`
}

func (s *Server) createDirectory(w http.ResponseWriter, r *http.Request) {
	var req mkdirRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		s.fail(w, r, "", "create_directory", badRequest("invalid json"))
		return
	}
	p, err := s.Launcher.Mkdir(req.Parent, req.Name)
	if err != nil {
		s.fail(w, r, "", "create_directory", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"path": p})
}

type archiveSessionsRequest struct {
	IDs []string `json:"ids"`
}

// archivedSession is an archive response entry.
type archivedSession struct {
	model.Session
	// Warning tells the user why the session's worktree was kept.
	Warning string `json:"warning,omitempty"`
}

// archiveOne stops a running session (closing its Herdr pane), removes the
// worktree it was started in when nothing there would be lost, and marks
// the returned model as archived. The archive store is updated by the
// caller so a batch can persist all ids in one write.
func (s *Server) archiveOne(ctx context.Context, id string) (archivedSession, bool, error) {
	sess, res, err := s.Sessions.Get(ctx, id)
	if err != nil {
		return archivedSession{}, false, err
	}
	stopped := res.Live != nil
	if res.Live != nil {
		if err := s.Launcher.Stop(ctx, res.Live.PaneID); err != nil {
			return archivedSession{}, false, err
		}
		sess.Status, sess.PaneID, sess.CanSend = model.StatusOffline, "", false
	}
	sess.Archived = true
	return archivedSession{Session: sess, Warning: s.removeWorktree(ctx, sess, res)}, stopped, nil
}

// removeWorktree deletes the session's worktree and returns a warning when
// it was kept or could not be removed.
func (s *Server) removeWorktree(ctx context.Context, sess model.Session, res *sessions.Resolved) string {
	var inUse []string
	if snap, err := s.Launcher.Herdr.Snapshot(ctx); err == nil {
		for _, pn := range snap.Panes {
			inUse = append(inUse, pn.WorkingDir())
		}
	} else {
		return "" // unknown which panes still use it; leave it alone
	}
	owner := worktree.Owner{Provider: res.Provider.Name(), NativeID: res.NativeID}
	out, err := worktree.Cleanup(ctx, sess.Cwd, owner, inUse)
	if err != nil {
		slog.Warn("removing worktree failed", "provider", owner.Provider, "session_id", sess.ID, "cwd", sess.Cwd, "error", err)
		return "Could not remove the session's worktree: " + err.Error()
	}
	if out == nil {
		return ""
	}
	slog.Info("session worktree cleaned up", "provider", owner.Provider, "session_id", sess.ID, "path", out.Path, "removed", out.Removed, "reason", out.Reason)
	return out.Warning()
}

// archiveSession stops a running session (closing its Herdr pane) and hides
// it from the session list.
func (s *Server) archiveSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, stopped, err := s.archiveOne(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "archive_session", err)
		return
	}
	if err := s.Archive.Add(id); err != nil {
		s.fail(w, r, id, "archive_session", err)
		return
	}
	slog.Info("session archived", "provider", sess.Provider, "session_id", id, "operation", "archive_session", "stopped", stopped)
	writeJSON(w, http.StatusOK, sess)
}

// archiveSessions archives several sessions in one request. Running sessions
// are stopped before the archive ids are persisted.
func (s *Server) archiveSessions(w http.ResponseWriter, r *http.Request) {
	var req archiveSessionsRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		s.fail(w, r, "", "archive_sessions", badRequest("invalid json"))
		return
	}

	ids := make([]string, 0, len(req.IDs))
	seen := make(map[string]struct{}, len(req.IDs))
	for _, id := range req.IDs {
		if id == "" {
			s.fail(w, r, "", "archive_sessions", badRequest("ids must not contain empty values"))
			return
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		s.fail(w, r, "", "archive_sessions", badRequest("ids must not be empty"))
		return
	}

	archived := make([]archivedSession, 0, len(ids))
	stopped := make([]bool, 0, len(ids))
	for _, id := range ids {
		sess, wasStopped, err := s.archiveOne(r.Context(), id)
		if err != nil {
			s.fail(w, r, id, "archive_sessions", err)
			return
		}
		archived = append(archived, sess)
		stopped = append(stopped, wasStopped)
	}
	if err := s.Archive.AddMany(ids); err != nil {
		s.fail(w, r, "", "archive_sessions", err)
		return
	}
	for i, sess := range archived {
		slog.Info("session archived", "provider", sess.Provider, "session_id", sess.ID, "operation", "archive_sessions", "stopped", stopped[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": archived})
}

func (s *Server) unarchiveSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Archive.Remove(id); err != nil {
		s.fail(w, r, id, "unarchive_session", err)
		return
	}
	sess, _, err := s.Sessions.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "unarchive_session", err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

type resumeRequest struct {
	Trust bool `json:"trust"`
}

// resumeSession reopens a session that is not running in Herdr and takes it
// out of the archive.
func (s *Server) resumeSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req resumeRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
			s.fail(w, r, id, "resume_session", badRequest("invalid json"))
			return
		}
	}
	sess, res, err := s.Sessions.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "resume_session", err)
		return
	}
	if res.Live != nil {
		s.fail(w, r, id, "resume_session", errAlreadyLive)
		return
	}
	out, err := s.Launcher.Resume(r.Context(), res.Provider.Name(), res.NativeID, sess.Cwd, req.Trust)
	if err != nil {
		s.Sink.Record(res.Provider.Name(), id, deadletter.SendError, "resume session: "+err.Error(), nil)
		s.fail(w, r, id, "resume_session", err)
		return
	}
	if err := s.Archive.Remove(id); err != nil {
		slog.Warn("unarchive after resume failed", "session_id", id, "error", err)
	}
	writeJSON(w, http.StatusCreated, out)
}

// getUsage serves the cached subscription limits; reading them takes several
// seconds, so the gateway refreshes them in the background.
func (s *Server) getUsage(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Usage.Snapshot())
}

// refreshUsage re-reads the limits now. Concurrent callers share one read.
func (s *Server) refreshUsage(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.Usage.Refresh(r.Context()))
}

// checkDirectory is read-only: even a mismatch never creates a workspace.
func (s *Server) checkDirectory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cwd    string `json:"cwd"`
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || req.Cwd == "" {
		writeError(w, http.StatusBadRequest, "cwd and a valid request are required")
		return
	}
	dir, err := s.Launcher.ResolveDir(req.Cwd)
	if err != nil {
		s.fail(w, r, "", "check_directory", err)
		return
	}
	writeJSON(w, http.StatusOK, s.DirectoryCheck.Check(r.Context(), dir, req.Prompt))
}
