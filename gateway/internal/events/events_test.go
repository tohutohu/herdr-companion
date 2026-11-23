package events

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

func recv(t *testing.T, s *Subscription) Event {
	t.Helper()
	select {
	case ev := <-s.Events():
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("no event")
		return Event{}
	}
}

func noEvent(t *testing.T, s *Subscription, wait time.Duration) {
	t.Helper()
	select {
	case ev := <-s.Events():
		t.Fatalf("unexpected event %s %s", ev.Type, ev.Data)
	case <-time.After(wait):
	}
}

func TestHub_再接続で取りこぼした分だけ送り直す(t *testing.T) {
	h := New()
	s, _, _ := h.Subscribe("")
	h.Publish(TypeSessions, struct{}{})
	first := recv(t, s)
	s.Close()
	h.Publish(TypeSession, SessionChange{SessionID: "claude:a"})
	h.Publish(TypeResident, struct{}{})
	h.PublishTransient(TypeStreaming, Streaming{SessionID: "claude:a", MessageID: "m", Text: "x"})

	_, replay, reset := h.Subscribe(first.ID)
	if reset {
		t.Fatal("reset")
	}
	if len(replay) != 2 || replay[0].Type != TypeSession || replay[1].Type != TypeResident {
		t.Fatalf("replay = %+v", replay)
	}
	if !strings.HasPrefix(replay[1].ID, h.Epoch()+"-") {
		t.Fatalf("id = %q", replay[1].ID)
	}
}

func TestHub_最新まで受け取っていれば何も送り直さない(t *testing.T) {
	h := New()
	h.Publish(TypeSessions, struct{}{})
	_, replay, reset := h.Subscribe(h.Epoch() + "-1")
	if reset || len(replay) != 0 {
		t.Fatalf("replay = %v reset = %v", replay, reset)
	}
}

func TestHub_別の起動や古すぎるIDはリセットを求める(t *testing.T) {
	h := New()
	h.backlog = 2
	for range 5 {
		h.Publish(TypeSessions, struct{}{})
	}
	for _, id := range []string{"other-1", h.Epoch() + "-1", h.Epoch() + "-99", "garbage"} {
		if _, _, reset := h.Subscribe(id); !reset {
			t.Errorf("%s: no reset", id)
		}
	}
	// The oldest kept event's predecessor can still be replayed from.
	if _, replay, reset := h.Subscribe(h.Epoch() + "-3"); reset || len(replay) != 2 {
		t.Fatalf("replay = %v reset = %v", replay, reset)
	}
}

func TestHub_遅いクライアントは切断される(t *testing.T) {
	h := New()
	s, _, _ := h.Subscribe("")
	for range subscriberBuffer + 1 {
		h.Publish(TypeSessions, struct{}{})
	}
	select {
	case <-s.Done():
	default:
		t.Fatal("slow subscriber kept")
	}
	if h.Subscribers() != 0 {
		t.Fatalf("subscribers = %d", h.Subscribers())
	}
}

func TestHub_購読者数の変化を知らせる(t *testing.T) {
	h := New()
	var mu sync.Mutex
	var counts []int
	h.OnSubscribers(func(n int) { mu.Lock(); counts = append(counts, n); mu.Unlock() })
	a, _, _ := h.Subscribe("")
	b, _, _ := h.Subscribe("")
	a.Close()
	b.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(counts) != 4 || counts[1] != 2 || counts[3] != 0 {
		t.Fatalf("counts = %v", counts)
	}
}

func TestStreamer_間隔内の差分はまとめて最後の全文を送る(t *testing.T) {
	h := New()
	s, _, _ := h.Subscribe("")
	st := &Streamer{Hub: h, Throttle: 50 * time.Millisecond}
	st.Report(Streaming{SessionID: "claude:a", MessageID: "m", Text: "a"})
	st.Report(Streaming{SessionID: "claude:a", MessageID: "m", Text: "ab"})
	st.Report(Streaming{SessionID: "claude:a", MessageID: "m", Text: "abc"})

	var got []Streaming
	for range 2 {
		var d Streaming
		ev := recv(t, s)
		if ev.ID != "" {
			t.Fatalf("streaming event has id %q", ev.ID)
		}
		json.Unmarshal(ev.Data, &d)
		got = append(got, d)
	}
	if got[0].Text != "a" || got[1].Text != "abc" {
		t.Fatalf("got %+v", got)
	}
	noEvent(t, s, 120*time.Millisecond)
}

func TestStreamer_完了はすぐ送り保留中の別メッセージを先に出す(t *testing.T) {
	h := New()
	s, _, _ := h.Subscribe("")
	st := &Streamer{Hub: h, Throttle: time.Hour}
	st.Report(Streaming{SessionID: "claude:a", MessageID: "m1", Text: "a"})
	st.Report(Streaming{SessionID: "claude:a", MessageID: "m1", Text: "ab"})
	st.Report(Streaming{SessionID: "claude:a", MessageID: "m2", Text: "x"})
	st.Report(Streaming{SessionID: "claude:a", MessageID: "m2", Done: true})

	want := []Streaming{
		{SessionID: "claude:a", MessageID: "m1", Text: "a"},
		{SessionID: "claude:a", MessageID: "m1", Text: "ab"},
		{SessionID: "claude:a", MessageID: "m2", Text: "x"},
		{SessionID: "claude:a", MessageID: "m2", Done: true},
	}
	for i, w := range want {
		var d Streaming
		json.Unmarshal(recv(t, s).Data, &d)
		if d != w {
			t.Fatalf("%d: got %+v want %+v", i, d, w)
		}
	}
}

type fakeLive struct {
	mu       sync.Mutex
	sessions []model.Session
}

func (f *fakeLive) set(s ...model.Session) { f.mu.Lock(); f.sessions = s; f.mu.Unlock() }
func (f *fakeLive) Live(context.Context) ([]model.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.Session(nil), f.sessions...), nil
}

func TestTracker_変わったセッションと消えたセッションを知らせる(t *testing.T) {
	h := New()
	live := &fakeLive{}
	tr := &Tracker{Hub: h, Live: live.Live}
	live.set(model.Session{ID: "claude:a", Status: model.StatusIdle}, model.Session{ID: "codex:b", Status: model.StatusIdle})
	tr.Evaluate(context.Background()) // the first read only records
	s, _, _ := h.Subscribe("")

	live.set(model.Session{ID: "claude:a", Status: model.StatusRunning, LastMessage: "hi"})
	if !tr.Evaluate(context.Background()) {
		t.Fatal("running not reported")
	}
	got := map[string]SessionChange{}
	for range 2 {
		var c SessionChange
		json.Unmarshal(recv(t, s).Data, &c)
		got[c.SessionID] = c
	}
	if got["claude:a"].Status != "running" || got["claude:a"].Removed || !got["codex:b"].Removed {
		t.Fatalf("got %+v", got)
	}

	tr.Evaluate(context.Background())
	noEvent(t, s, 50*time.Millisecond)
}

func TestTracker_実行中で接続があれば短い間隔で読み直す(t *testing.T) {
	h := New()
	live := &fakeLive{}
	live.set(model.Session{ID: "claude:a", Status: model.StatusRunning})
	tr := &Tracker{Hub: h, Live: live.Live, Debounce: time.Millisecond, Interval: 10 * time.Millisecond, Resync: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tr.Run(ctx)
	time.Sleep(50 * time.Millisecond) // the first read is done
	s, _, _ := h.Subscribe("")
	defer s.Close()
	time.Sleep(50 * time.Millisecond) // the read the subscription asked for
	live.set(model.Session{ID: "claude:a", Status: model.StatusRunning, LastMessage: "tool result"})
	ev := recv(t, s)
	if ev.Type != TypeSession {
		t.Fatalf("type = %s", ev.Type)
	}
}
