package api

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/events"
)

type sseEvent struct{ id, typ, data string }

// openEvents connects to /v1/events and returns a channel of its events.
func openEvents(t *testing.T, url, token, lastID string) <-chan sseEvent {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", url+"/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	out := make(chan sseEvent, 64)
	go func() {
		defer resp.Body.Close()
		defer close(out)
		sc := bufio.NewScanner(resp.Body)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if ev.typ != "" {
					out <- ev
				}
				ev = sseEvent{}
			case strings.HasPrefix(line, "id: "):
				ev.id = line[4:]
			case strings.HasPrefix(line, "event: "):
				ev.typ = line[7:]
			case strings.HasPrefix(line, "data: "):
				ev.data = line[6:]
			}
		}
	}()
	return out
}

func next(t *testing.T, ch <-chan sseEvent) sseEvent {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("stream closed")
		}
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("no event")
		return sseEvent{}
	}
}

func TestEvents_接続するとreadyが届き発行したイベントが流れる(t *testing.T) {
	hub := events.New()
	ts, _, _, tok := newTestServer(t, func(s *Server) { s.Events = hub })
	ch := openEvents(t, ts.URL, tok, "")
	ready := next(t, ch)
	if ready.typ != "ready" || !strings.Contains(ready.data, `"reset":false`) || !strings.Contains(ready.data, hub.Epoch()) {
		t.Fatalf("ready = %+v", ready)
	}
	hub.Publish(events.TypeSession, events.SessionChange{SessionID: "fake:s1", Status: "running"})
	hub.PublishTransient(events.TypeStreaming, events.Streaming{SessionID: "fake:s1", MessageID: "m", Text: "こんにちは"})
	ev := next(t, ch)
	if ev.typ != "session" || ev.id != hub.Epoch()+"-1" || !strings.Contains(ev.data, `"sessionId":"fake:s1"`) {
		t.Fatalf("session = %+v", ev)
	}
	ev = next(t, ch)
	if ev.typ != "streaming" || ev.id != "" || !strings.Contains(ev.data, "こんにちは") {
		t.Fatalf("streaming = %+v", ev)
	}
}

func TestEvents_LastEventIDで取りこぼした分が届き古い起動ならリセット(t *testing.T) {
	hub := events.New()
	ts, _, _, tok := newTestServer(t, func(s *Server) { s.Events = hub })
	hub.Publish(events.TypeSessions, struct{}{})
	hub.Publish(events.TypeResident, struct{}{})

	ch := openEvents(t, ts.URL, tok, hub.Epoch()+"-1")
	if ready := next(t, ch); !strings.Contains(ready.data, `"reset":false`) {
		t.Fatalf("ready = %+v", ready)
	}
	if ev := next(t, ch); ev.typ != "resident" || ev.id != hub.Epoch()+"-2" {
		t.Fatalf("replay = %+v", ev)
	}

	ch = openEvents(t, ts.URL, tok, "previous-run-7")
	if ready := next(t, ch); !strings.Contains(ready.data, `"reset":true`) {
		t.Fatalf("ready = %+v", ready)
	}
}

func TestEvents_トークンなしは401(t *testing.T) {
	hub := events.New()
	ts, _, _, _ := newTestServer(t, func(s *Server) { s.Events = hub })
	resp, _ := do(t, ts, "", "GET", "/v1/events", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestEvents_APIでの変更はセッション一覧の変化として知らせる(t *testing.T) {
	hub := events.New()
	kicks := 0
	ts, _, _, tok := newTestServer(t, func(s *Server) { s.Events = hub; s.Kick = func() { kicks++ } })
	ch := openEvents(t, ts.URL, tok, "")
	next(t, ch) // ready

	resp, body := do(t, ts, tok, "POST", "/v1/sessions/fake:s1/archive", nil, "")
	if resp.StatusCode != 200 {
		t.Fatalf("archive %d %s", resp.StatusCode, body)
	}
	if ev := next(t, ch); ev.typ != "sessions" {
		t.Fatalf("event = %+v", ev)
	}
	// Reads and failed changes publish nothing.
	do(t, ts, tok, "GET", "/v1/sessions", nil, "")
	do(t, ts, tok, "POST", "/v1/sessions/fake:missing/interrupt", nil, "")
	do(t, ts, tok, "POST", "/v1/uploads", []byte("x"), "application/octet-stream")
	hub.Publish(events.TypeResident, struct{}{})
	if ev := next(t, ch); ev.typ != "resident" {
		t.Fatalf("event = %+v", ev)
	}
	if kicks == 0 {
		t.Fatal("tracker not kicked")
	}
}

func TestEvents_機能一覧でイベントの対応を伝える(t *testing.T) {
	ts, _, _, tok := newTestServer(t, func(s *Server) { s.Events = events.New() })
	_, body := do(t, ts, tok, "GET", "/v1/capabilities", nil, "")
	if !strings.Contains(string(body), `"events":true`) {
		t.Fatalf("capabilities = %s", body)
	}
}
