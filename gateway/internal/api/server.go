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
	"net/http/pprof"
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
	"github.com/tohutohu/herdr-android-client/gateway/internal/forward"
	"github.com/tohutohu/herdr-android-client/gateway/internal/launcher"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/resident"
	"github.com/tohutohu/herdr-android-client/gateway/internal/runtime"
	"github.com/tohutohu/herdr-android-client/gateway/internal/sessions"
	"github.com/tohutohu/herdr-android-client/gateway/internal/uploads"
	"github.com/tohutohu/herdr-android-client/gateway/internal/usage"
	"github.com/tohutohu/herdr-android-client/gateway/internal/worktree"
)

type Server struct {
	Translation     Translator
	pairing         pairingState
	MessageDelivery *MessageDeliveryStore
	AgentUpdates    *agentupdate.Service
	DirectoryCheck  *directorycheck.Checker
	Sessions        *sessions.Service
	Uploads         *uploads.Store
	Config          *config.Store
	Sink            deadletter.Sink
	Launcher        *launcher.Launcher
	Archive         *archive.Store
	// Usage is nil when subscription limit reporting is turned off.
	Usage *usage.Service
	// Direct runs the sessions.
	Direct *runtime.Manager
	// Resident owns the resident tasks; nil leaves /v1/resident out (and
	// /v1/capabilities says so).
	Resident *resident.Service
	// Listen is the address the server listens on, for telling clients behind
	// a tunnel where to reach the localhost proxy directly.
	Listen string
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /pair", s.redeemPairing)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	api := http.NewServeMux()
	api.HandleFunc("POST /v1/pairing", s.createPairing)
	api.HandleFunc("DELETE /v1/pairing", s.cancelPairing)
	api.HandleFunc("GET /v1/translation", s.translationStatus)
	api.HandleFunc("POST /v1/translation", s.configureTranslation)
	api.HandleFunc("GET /v1/sessions", s.listSessions)
	api.HandleFunc("POST /v1/sessions", s.startSession)
	api.HandleFunc("POST /v1/sessions/archive", s.archiveSessions)
	api.HandleFunc("POST /v1/launches/{launch}/trust", s.answerTrust)
	api.HandleFunc("GET /v1/models", s.listModels)
	api.HandleFunc("GET /v1/agents", s.listAgents)
	api.HandleFunc("POST /v1/agents/{provider}/update", s.updateAgent)
	api.HandleFunc("GET /v1/directories", s.listDirectories)
	api.HandleFunc("POST /v1/directories", s.createDirectory)
	api.HandleFunc("POST /v1/directories/check", s.checkDirectory)
	api.HandleFunc("GET /v1/sessions/{id}", s.getSession)
	api.HandleFunc("POST /v1/sessions/{id}/archive", s.archiveSession)
	api.HandleFunc("DELETE /v1/sessions/{id}/archive", s.unarchiveSession)
	api.HandleFunc("POST /v1/sessions/{id}/resume", s.resumeSession)
	api.HandleFunc("GET /v1/sessions/{id}/messages", s.getMessages)
	api.HandleFunc("POST /v1/sessions/{id}/messages", s.postMessage)
	api.HandleFunc("GET /v1/message-delivery", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"idempotency": s.MessageDelivery != nil})
	})
	api.HandleFunc("POST /v1/sessions/{id}/respond", s.respond)
	api.HandleFunc("POST /v1/sessions/{id}/mode", s.cycleMode)
	api.HandleFunc("POST /v1/sessions/{id}/model", s.setModel)
	api.HandleFunc("POST /v1/sessions/{id}/effort", s.setEffort)
	api.HandleFunc("POST /v1/sessions/{id}/interrupt", s.interrupt)
	api.HandleFunc("POST /v1/sessions/{id}/side-question", s.sideQuestion)
	api.HandleFunc("POST /v1/sessions/{id}/seen", s.markSeen)
	api.HandleFunc("POST /v1/sessions/{id}/reload", s.reloadSession)
	api.HandleFunc("GET /v1/sessions/{id}/messages/{mid}/images/{n}", s.getImage)
	api.HandleFunc("GET /v1/sessions/{id}/files", s.listFiles)
	api.HandleFunc("GET /v1/sessions/{id}/files/content", s.fileContent)
	api.HandleFunc("GET /v1/sessions/{id}/files/stat", s.fileStat)
	api.HandleFunc("POST /v1/uploads", s.upload)
	api.HandleFunc("GET /v1/usage", s.getUsage)
	api.HandleFunc("POST /v1/usage/refresh", s.refreshUsage)
	s.residentRoutes(api)
	api.HandleFunc("POST /v1/devices", s.registerDevice)
	api.HandleFunc("DELETE /v1/devices", s.unregisterDevice)
	mux.Handle("/v1/", s.auth(api))
	// Profiles contain runtime details and require the same token as the API.
	mux.Handle("GET /debug/pprof/", s.auth(http.HandlerFunc(pprof.Index)))
	mux.Handle("GET /debug/pprof/profile", s.auth(http.HandlerFunc(pprof.Profile)))
	mux.Handle("GET /debug/pprof/trace", s.auth(http.HandlerFunc(pprof.Trace)))
	mux.Handle("GET /debug/pprof/symbol", s.auth(http.HandlerFunc(pprof.Symbol)))
	logged := logRequests(mux)
	// Proxy requests for this Mac's localhost (docs/api.md#localhost-の転送) never reach the API
	// paths. They are left out of the request log, as a page loads many of them.
	proxy := &forward.Proxy{Token: func() string { return s.Config.Get().AuthToken }}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if forward.IsProxyRequest(r) {
			proxy.ServeHTTP(w, r)
			return
		}
		logged.ServeHTTP(w, r)
	})
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
	switch {
	case errors.Is(err, providers.ErrNotFound), errors.Is(err, files.ErrNotFound), errors.Is(err, uploads.ErrNotFound),
		errors.Is(err, launcher.ErrNoPendingTrust):
		status = http.StatusNotFound
	case errors.Is(err, providers.ErrRuntimeUnavailable):
		status = http.StatusServiceUnavailable
	case errors.Is(err, runtime.ErrNotManaged):
		status = http.StatusConflict
	case errors.Is(err, errInvalidSetting):
		status = http.StatusBadRequest
	case errors.Is(err, providers.ErrNotLive), errors.Is(err, providers.ErrInteractionGone), errors.Is(err, providers.ErrNoConversation),
		errors.Is(err, errAlreadyLive), errors.Is(err, launcher.ErrNoCwd), errors.Is(err, launcher.ErrCwdGone):
		status = http.StatusConflict
	case errors.Is(err, providers.ErrUnsupported):
		status = http.StatusUnprocessableEntity
	case errors.Is(err, files.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, files.ErrTooLarge), errors.Is(err, uploads.ErrTooLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, files.ErrNoRoot):
		status = http.StatusConflict
	case errors.Is(err, errBadRequest), errors.Is(err, launcher.ErrInvalidName), errors.Is(err, launcher.ErrUnknownProvider),
		errors.Is(err, launcher.ErrInvalidModel), errors.Is(err, launcher.ErrInvalidEffort), errors.Is(err, launcher.ErrInvalidMode),
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
	errAlreadyLive = errors.New("session is already running")
)

func badRequest(msg string) error { return errors.Join(errBadRequest, errors.New(msg)) }

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("archived") == "true" {
		s.listArchived(w, r)
		return
	}
	list, err := s.Sessions.List(r.Context())
	if err != nil {
		s.fail(w, r, "", "list_sessions", err)
		return
	}
	if list == nil {
		list = []model.Session{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": s.translatedSessions(list)})
}

// listArchived lists archived sessions. With limit, it returns one page and
// nextOffset when more remain; without it, every archived session.
func (s *Server) listArchived(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var offset, limit int
	for name, v := range map[string]*int{"offset": &offset, "limit": &limit} {
		raw := q.Get(name)
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			s.fail(w, r, "", "list_archived", badRequest(name+" must be a non-negative integer"))
			return
		}
		*v = n
	}
	list, next := s.Sessions.Archived(r.Context(), offset, limit)
	if list == nil {
		list = []model.Session{}
	}
	resp := map[string]any{"sessions": s.translatedSessions(list)}
	if next > 0 {
		resp["nextOffset"] = next
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, _, err := s.Sessions.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "get_session", err)
		return
	}
	writeJSON(w, http.StatusOK, s.translatedSession(sess))
}

// getMessages returns the session plus messages. With ?after=<id> only the
// messages from that id onward are returned (the anchor is included so that
// in-progress updates to it are seen). Unknown or queued anchors return everything:
// queued prompts float at the tail, so new messages can appear before them.
func (s *Server) getMessages(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, msgs, err := s.Sessions.Conversation(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "get_messages", err)
		return
	}
	if s.Translation != nil {
		msgs = s.Translation.Messages(id, msgs, sess.Status != model.StatusRunning, s.sessionTranslation(id))
	}
	mode := s.sessionTranslation(id)
	if s.Translation == nil || (!s.Translation.Status().OutputEverEnabled && !s.Translation.Status().InputEverEnabled && (mode == nil || !*mode)) {
		msgs = messagesFrom(msgs, r.URL.Query().Get("after"))
	}
	if msgs == nil {
		msgs = []model.Message{}
	}
	writeJSONWithETag(w, r, map[string]any{"session": s.translatedSession(sess), "messages": msgs})
}

func messagesFrom(msgs []model.Message, after string) []model.Message {
	if after == "" || strings.HasPrefix(after, "queued:") {
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
	Text      string   `json:"text"`
	Uploads   []string `json:"uploads"`
	RequestID string   `json:"requestId,omitempty"`
}

// sendTimeout bounds a send that no longer has a client waiting on it, which
// may first resume the session in a new process.
const sendTimeout = 2 * time.Minute

// uploadPaths resolves upload ids to the stored files' paths, split into
// images and other files.
func (s *Server) uploadPaths(ids []string) (images, files []string, err error) {
	for _, u := range ids {
		f, err := s.Uploads.Get(u)
		if err != nil {
			return nil, nil, err
		}
		if f.IsImage() {
			images = append(images, f.Path)
		} else {
			files = append(files, f.Path)
		}
	}
	return images, files, nil
}

func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req sendRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		s.fail(w, r, id, "send_message", badRequest("invalid json"))
		return
	}
	var deliveryDigest, deliveryScope string
	if req.RequestID != "" {
		if !deliveryID.MatchString(req.RequestID) {
			writeError(w, http.StatusBadRequest, "invalid request ID")
			return
		}
		if s.MessageDelivery == nil {
			writeError(w, http.StatusServiceUnavailable, "message delivery receipts unavailable")
			return
		}
		// Include session and the complete payload; never store message contents.
		payload, _ := json.Marshal(struct {
			Session string
			Request sendRequest
		}{id, req})
		digest := sha256.Sum256(payload)
		deliveryDigest = hex.EncodeToString(digest[:])
		deliveryScope = config.GatewayID(s.Config.Get().AuthToken)
		existing, err := s.MessageDelivery.previous(deliveryScope, req.RequestID, deliveryDigest)
		if err != nil {
			if errors.Is(err, errDeliveryUnknown) || errors.Is(err, errDeliveryConflict) {
				writeError(w, http.StatusConflict, err.Error())
			} else {
				writeError(w, http.StatusServiceUnavailable, "could not persist message delivery receipt")
			}
			return
		}
		if existing {
			writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
			return
		}
	}
	in := model.Input{Text: req.Text}
	var err error
	if in.Images, in.Files, err = s.uploadPaths(req.Uploads); err != nil {
		s.fail(w, r, id, "send_message", err)
		return
	}
	if in.Empty() {
		s.fail(w, r, id, "send_message", badRequest("empty message"))
		return
	}
	res, err := s.Sessions.Resolve(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "send_message", err)
		return
	}
	if res.Live == nil {
		s.fail(w, r, id, "send_message", providers.ErrNotLive)
		return
	}

	if in.Text, err = s.translateInput(r.Context(), id, in.Text, in.Files); err != nil {
		writeError(w, http.StatusServiceUnavailable, "プロンプトを翻訳できませんでした（未送信）: "+err.Error())
		return
	}
	if req.RequestID != "" {
		fresh, err := s.MessageDelivery.claim(deliveryScope, req.RequestID, deliveryDigest)
		if err != nil {
			if errors.Is(err, errDeliveryUnknown) || errors.Is(err, errDeliveryConflict) {
				writeError(w, http.StatusConflict, err.Error())
			} else {
				writeError(w, http.StatusServiceUnavailable, "could not persist message delivery receipt")
			}
			return
		}
		if !fresh {
			writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
			return
		}
	}
	// Once the message is here it goes to the agent even if the phone hangs up
	// (leaves the screen, loses the network): a client that retries could
	// otherwise send it twice.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), sendTimeout)
	defer cancel()
	if err := s.Sessions.Send(ctx, res, in); err != nil {
		s.recordSendError(id, "send message: "+err.Error(), req, err)
		if req.RequestID != "" {
			writeError(w, http.StatusConflict, errDeliveryUnknown.Error())
			return
		}
		s.fail(w, r, id, "send_message", err)
		return
	}
	if req.RequestID != "" {
		if err := s.MessageDelivery.accept(deliveryScope, req.RequestID, deliveryDigest); err != nil {
			writeError(w, http.StatusConflict, errDeliveryUnknown.Error())
			return
		}
	}
	if s.Archive != nil {
		if err := s.Archive.Remove(id); err != nil {
			// The message has already been sent: returning an error here
			// could make a client retry it and send the same prompt twice.
			slog.Warn("unarchive after send failed", "session_id", id, "error", err)
		}
	}
	response := map[string]any{"ok": true}
	if in.Text != req.Text {
		// Let the sending client reconcile its Japanese pending bubble with
		// the provider's English echo even before display enrichment arrives.
		response["sentText"] = in.Text
	}
	writeJSON(w, http.StatusAccepted, response)
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
	if res.Live == nil {
		s.fail(w, r, id, "respond", providers.ErrNotLive)
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

// cycleMode sets the session's mode to a mode id from the catalog, or, with
// no mode, advances it to the next one.
func (s *Server) cycleMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
			s.fail(w, r, r.PathValue("id"), "cycle_mode", badRequest("invalid json"))
			return
		}
	}
	id, provider, ok := s.liveSession(w, r, "cycle_mode")
	if !ok {
		return
	}
	if req.Mode != "" {
		if err := s.validSetting(r.Context(), provider, "mode", req.Mode, ""); err != nil {
			s.fail(w, r, id, "cycle_mode", err)
			return
		}
	}
	if err := s.Direct.SetMode(r.Context(), id, req.Mode); err != nil {
		s.fail(w, r, id, "cycle_mode", err)
		return
	}
	s.Sessions.Forget(id)
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

// markSeen turns a finished turn the user has now looked at into idle, so
// the session is no longer unread.
func (s *Server) markSeen(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := s.Sessions.Resolve(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "mark_seen", err)
		return
	}
	if res.Live != nil {
		s.Direct.MarkSeen(id)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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

// upload accepts the raw file body with its Content-Type. The client may name
// the file with a Content-Disposition header; without one the name is derived
// from the content type.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > uploads.MaxUploadSize {
		s.fail(w, r, "", "upload", uploads.ErrTooLarge)
		return
	}
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

type startRequest struct {
	launcher.StartRequest
	// Uploads are attached to the first prompt.
	Uploads []string `json:"uploads,omitempty"`
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || req.Provider == "" || req.Cwd == "" {
		s.fail(w, r, "", "start_session", badRequest("provider and cwd are required"))
		return
	}
	var err error
	if req.Images, req.Files, err = s.uploadPaths(req.Uploads); err != nil {
		s.fail(w, r, "", "start_session", err)
		return
	}

	if req.Prompt, err = s.translateInput(r.Context(), "", req.Prompt, req.Files, req.Translation); err != nil {
		writeError(w, http.StatusServiceUnavailable, "プロンプトを翻訳できませんでした（未送信）: "+err.Error())
		return
	}
	res, err := s.Launcher.Start(r.Context(), req.StartRequest)
	if err != nil {
		s.Sink.Record(req.Provider, "", deadletter.SendError, "start session: "+err.Error(), req)
		s.fail(w, r, "", "start_session", err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

// answerTrust continues (or, when declined, cancels) a new session that
// waits on the user's folder-trust answer.
func (s *Server) answerTrust(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Trust bool `json:"trust"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		s.fail(w, r, "", "answer_trust", badRequest("invalid json"))
		return
	}
	res, err := s.Launcher.AnswerTrust(r.Context(), r.PathValue("launch"), req.Trust)
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

// archiveOne stops a running session, removes the worktree it was started
// in when nothing there would be lost, and marks the returned model as
// archived. The archive store is updated by the caller so a batch can
// persist all ids in one write.
func (s *Server) archiveOne(ctx context.Context, id string) (archivedSession, bool, error) {
	sess, res, err := s.Sessions.Get(ctx, id)
	if err != nil {
		return archivedSession{}, false, err
	}
	stopped := res.Live != nil && !res.Live.Dormant
	if res.Live != nil {
		if err := s.Direct.Forget(ctx, id); err != nil {
			return archivedSession{}, false, err
		}
		sess.Status, sess.CanSend, sess.Unread = model.StatusOffline, false, false
		sess.PendingModel, sess.PendingEffort, sess.SettingsApply = "", "", ""
	}
	sess.Archived = true
	return archivedSession{Session: sess, Warning: s.removeWorktree(ctx, sess)}, stopped, nil
}

// removeWorktree removes the session's worktree and returns a warning when
// it was kept or could not be removed.
func (s *Server) removeWorktree(ctx context.Context, sess model.Session) string {
	out, err := worktree.Cleanup(ctx, sess.Cwd, func(path string) bool { return s.worktreeInUse(ctx, sess.ID, path) })
	if err != nil {
		slog.Warn("removing worktree failed", "provider", sess.Provider, "session_id", sess.ID, "cwd", sess.Cwd, "error", err)
		return "Could not remove the session's worktree: " + err.Error()
	}
	if out == nil {
		return ""
	}
	slog.Info("session worktree cleaned up", "provider", sess.Provider, "session_id", sess.ID, "path", out.Path, "removed", out.Removed, "reason", out.Reason)
	return out.Warning()
}

// archiveSession stops a running session and hides it from the session list.
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
	sess.Session = s.translatedSession(sess.Session)
	writeJSON(w, http.StatusOK, sess)
}

// archiveSessions archives several sessions in one request. Running sessions
// are stopped before the archive ids are persisted. When one fails, the ones
// already stopped are still archived so they do not linger, stopped, in the
// session list.
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
			if len(archived) > 0 {
				done := make([]string, len(archived))
				for i, a := range archived {
					done[i] = a.ID
				}
				if aerr := s.Archive.AddMany(done); aerr != nil {
					slog.Warn("archiving the sessions before a failed one failed", "operation", "archive_sessions", "error", aerr)
				}
			}
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
	writeJSON(w, http.StatusOK, s.translatedSession(sess))
}

// resumeSession reopens a session that runs nowhere, in a new process under
// the same id, and takes it out of the archive.
func (s *Server) resumeSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, res, err := s.Sessions.Get(r.Context(), id)
	if err != nil {
		s.fail(w, r, id, "resume_session", err)
		return
	}
	if res.Live != nil && !res.Live.Dormant {
		s.fail(w, r, id, "resume_session", errAlreadyLive)
		return
	}
	out, err := s.Launcher.Resume(r.Context(), res.Provider.Name(), res.NativeID, sess.Cwd)
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
