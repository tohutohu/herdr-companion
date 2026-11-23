package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/events"
)

// eventsHeartbeat keeps idle streams open through proxies and tunnels and
// lets clients notice a dead connection by their read timeout.
const eventsHeartbeat = 20 * time.Second

// streamEvents serves GET /v1/events as Server-Sent Events (docs/api.md#イベント).
// The last event id comes from Last-Event-ID or ?lastEventId=.
func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	lastID := r.Header.Get("Last-Event-ID")
	if lastID == "" {
		lastID = r.URL.Query().Get("lastEventId")
	}
	sub, replay, reset := s.Events.Subscribe(lastID)
	defer sub.Close()

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// The ready event tells the client the stream is live; reset says it
	// must refetch everything because what it missed cannot be replayed.
	fmt.Fprintf(w, "retry: 3000\n\n")
	writeEvent(w, events.Event{Type: "ready", Data: []byte(fmt.Sprintf(`{"epoch":%q,"reset":%t}`, s.Events.Epoch(), reset))})
	for _, ev := range replay {
		writeEvent(w, ev)
	}
	if err := rc.Flush(); err != nil {
		return
	}
	heartbeat := time.NewTicker(eventsHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.Done():
			return
		case ev := <-sub.Events():
			writeEvent(w, ev)
			// Send what else is waiting in the same write.
		drain:
			for {
				select {
				case ev := <-sub.Events():
					writeEvent(w, ev)
				default:
					break drain
				}
			}
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}

func writeEvent(w http.ResponseWriter, ev events.Event) {
	var b strings.Builder
	if ev.ID != "" {
		b.WriteString("id: " + ev.ID + "\n")
	}
	b.WriteString("event: " + ev.Type + "\n")
	// JSON has no raw newlines, so one data line is enough.
	b.WriteString("data: ")
	b.Write(ev.Data)
	b.WriteString("\n\n")
	w.Write([]byte(b.String()))
}

// publishMutations tells connected apps that a successful change through the
// API may have changed the session list or resident tasks. Changes of live
// sessions are also published by the tracker; this covers the rest
// (archive, notes, favorites, offline sessions).
func (s *Server) publishMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if r.Method == http.MethodGet || r.Method == http.MethodHead || rec.status >= 400 {
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/resident/"):
			s.Events.Publish(events.TypeResident, struct{}{})
		case mutatesSessions(r.URL.Path):
			s.Events.Publish(events.TypeSessions, struct{}{})
		}
		if s.Kick != nil {
			s.Kick()
		}
	})
}

// mutatesSessions reports whether a non-GET request to path can change what
// the session list shows. Uploads, devices, translation of a text,
// pairing, usage reads and side questions cannot.
func mutatesSessions(path string) bool {
	for _, p := range []string{"/v1/uploads", "/v1/devices", "/v1/pairing", "/v1/translation/text", "/v1/usage", "/v1/directories"} {
		if path == p || strings.HasPrefix(path, p+"/") {
			return false
		}
	}
	return !strings.HasSuffix(path, "/side-question") && !strings.HasSuffix(path, "/reload")
}
