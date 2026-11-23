package events

import (
	"sync"
	"time"
)

// Streamer publishes TypeStreaming events for replies agents are writing, at
// most one per session every Throttle: an agent sends many small deltas, and
// each event carries the whole text so far, so the last one in a window is
// enough. A closing event is sent at once.
type Streamer struct {
	Hub      *Hub
	Throttle time.Duration

	mu      sync.Mutex
	pending map[string]*streamState // by session id
}

type streamState struct {
	latest Streaming
	// dirty means latest has not been published yet.
	dirty bool
	// timer is set while a window is open.
	timer *time.Timer
}

// Report records the text so far of a reply. It never blocks on clients.
func (s *Streamer) Report(ev Streaming) {
	if s.Hub == nil || ev.SessionID == "" || ev.MessageID == "" {
		return
	}
	s.mu.Lock()
	if s.pending == nil {
		s.pending = map[string]*streamState{}
	}
	st := s.pending[ev.SessionID]
	if st == nil {
		st = &streamState{}
		s.pending[ev.SessionID] = st
	}
	if ev.Done {
		// Send what the window held for another message first, then close.
		var flush *Streaming
		if st.dirty && st.latest.MessageID != ev.MessageID {
			f := st.latest
			flush = &f
		}
		if st.timer != nil {
			st.timer.Stop()
		}
		delete(s.pending, ev.SessionID)
		s.mu.Unlock()
		if flush != nil {
			s.Hub.PublishTransient(TypeStreaming, *flush)
		}
		s.Hub.PublishTransient(TypeStreaming, ev)
		return
	}
	if st.dirty && st.latest.MessageID != ev.MessageID {
		// A new message while the previous one waits: send both now.
		prev := st.latest
		st.latest, st.dirty = ev, false
		s.mu.Unlock()
		s.Hub.PublishTransient(TypeStreaming, prev)
		s.Hub.PublishTransient(TypeStreaming, ev)
		return
	}
	st.latest = ev
	if st.timer != nil {
		// A window is open: its end publishes this.
		st.dirty = true
		s.mu.Unlock()
		return
	}
	// Publish now and open a window.
	st.dirty = false
	st.timer = time.AfterFunc(s.throttle(), func() { s.windowEnd(ev.SessionID, st) })
	s.mu.Unlock()
	s.Hub.PublishTransient(TypeStreaming, ev)
}

func (s *Streamer) windowEnd(session string, st *streamState) {
	s.mu.Lock()
	if s.pending[session] != st {
		s.mu.Unlock()
		return
	}
	if !st.dirty {
		st.timer = nil
		s.mu.Unlock()
		return
	}
	ev := st.latest
	st.dirty = false
	st.timer = time.AfterFunc(s.throttle(), func() { s.windowEnd(session, st) })
	s.mu.Unlock()
	s.Hub.PublishTransient(TypeStreaming, ev)
}

func (s *Streamer) throttle() time.Duration {
	return orDefault(s.Throttle, 150*time.Millisecond)
}
