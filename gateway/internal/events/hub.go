// Package events tells connected apps what changed, over Server-Sent Events
// (GET /v1/events). Events are hints: an app refetches through the REST API,
// which stays the only source of the data itself.
//
// Each event a client may need to catch up on carries an id "<epoch>-<seq>".
// The epoch is new each time the Gateway starts, and only the last few
// hundred events are kept in memory, so a client that reconnects with an id
// from another epoch, or one too old, is told to reset (refetch everything)
// instead of being replayed. Providers stay the source of truth, so nothing
// here is persisted.
package events

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
)

// Event types.
const (
	// TypeSession: one session's state or conversation changed. Data: SessionChange.
	TypeSession = "session"
	// TypeSessions: the session list changed in a way not tied to one live
	// session (archive, a start, notes, …). Data: {}.
	TypeSessions = "sessions"
	// TypeResident: resident tasks or their runs changed. Data: {}.
	TypeResident = "resident"
	// TypeStreaming: the text so far of a reply an agent is writing. Not
	// replayed and without an id. Data: Streaming.
	TypeStreaming = "streaming"
)

// SessionChange is the data of a TypeSession event.
type SessionChange struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status,omitempty"`
	// Removed means the session left the Gateway's runtime (archived,
	// forgotten); the list should be refetched.
	Removed bool `json:"removed,omitempty"`
}

// Streaming is the data of a TypeStreaming event. Text is the whole reply so
// far, not a delta, so a client that missed one event loses nothing. Done
// means the reply is closed and the conversation has it (or will on the next
// fetch); the client should drop its streamed copy once it has fetched.
type Streaming struct {
	SessionID string `json:"sessionId"`
	MessageID string `json:"messageId"`
	Text      string `json:"text,omitempty"`
	Done      bool   `json:"done,omitempty"`
}

// Event is one message on the stream. ID is empty for events that are not
// replayed.
type Event struct {
	ID   string
	Type string
	Data json.RawMessage
}

// DefaultBacklog is how many replayable events are kept for reconnects.
const DefaultBacklog = 512

// subscriberBuffer is how many events may wait for a slow client before it
// is disconnected (it then reconnects and catches up or resets).
const subscriberBuffer = 256

// Hub fans events out to subscribers. The zero value is not usable; use New.
type Hub struct {
	epoch   string
	backlog int

	mu     sync.Mutex
	seq    uint64
	ring   []Event // replayable events, oldest first, at most backlog
	subs   map[*Subscription]struct{}
	closed bool
	// watchers are told when the number of subscribers changes.
	watchers []func(int)
}

func New() *Hub {
	var b [6]byte
	rand.Read(b[:])
	return &Hub{epoch: hex.EncodeToString(b[:]), backlog: DefaultBacklog, subs: map[*Subscription]struct{}{}}
}

// Epoch identifies this run of the Gateway.
func (h *Hub) Epoch() string { return h.epoch }

// Subscription is one connected client.
type Subscription struct {
	hub *Hub
	ch  chan Event
	// done is closed when the hub drops the subscription (slow client or
	// shutdown).
	done chan struct{}
	once sync.Once
}

// Events delivers the events published after Subscribe returned.
func (s *Subscription) Events() <-chan Event { return s.ch }

// Done is closed when the hub has dropped this subscription.
func (s *Subscription) Done() <-chan struct{} { return s.done }

// Close ends the subscription. Safe to call more than once.
func (s *Subscription) Close() {
	h := s.hub
	h.mu.Lock()
	delete(h.subs, s)
	n := len(h.subs)
	watchers := h.watchers
	h.mu.Unlock()
	s.drop()
	// Also when the hub had already dropped it: the count changed then.
	for _, w := range watchers {
		w(n)
	}
}

func (s *Subscription) drop() { s.once.Do(func() { close(s.done) }) }

// Subscribe registers a client. lastID is the id of the last event it
// received ("" for a new client). It returns the events it missed, or
// reset=true when they cannot be replayed (another epoch, too old, unknown),
// in which case the client must refetch everything. A new client (lastID "")
// gets neither: it is about to fetch anyway.
func (h *Hub) Subscribe(lastID string) (s *Subscription, replay []Event, reset bool) {
	s = &Subscription{hub: h, ch: make(chan Event, subscriberBuffer), done: make(chan struct{})}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		s.drop()
		return s, nil, false
	}
	if lastID != "" {
		replay, reset = h.sinceLocked(lastID)
	}
	h.subs[s] = struct{}{}
	n := len(h.subs)
	watchers := h.watchers
	h.mu.Unlock()
	for _, w := range watchers {
		w(n)
	}
	return s, replay, reset
}

func (h *Hub) sinceLocked(lastID string) ([]Event, bool) {
	epoch, seqText, ok := strings.Cut(lastID, "-")
	if !ok || epoch != h.epoch {
		return nil, true
	}
	seq, err := strconv.ParseUint(seqText, 10, 64)
	if err != nil || seq > h.seq {
		return nil, true
	}
	if seq == h.seq {
		return nil, false
	}
	// The ring holds consecutive sequence numbers ending at h.seq.
	oldest := h.seq - uint64(len(h.ring)) + 1
	if len(h.ring) == 0 || seq+1 < oldest {
		return nil, true
	}
	start := int(seq + 1 - oldest)
	return append([]Event(nil), h.ring[start:]...), false
}

// Publish sends a replayable event to every subscriber.
func (h *Hub) Publish(typ string, data any) {
	h.publish(typ, data, true)
}

// PublishTransient sends an event that is not replayed and has no id.
func (h *Hub) PublishTransient(typ string, data any) {
	h.publish(typ, data, false)
}

func (h *Hub) publish(typ string, data any, keep bool) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	ev := Event{Type: typ, Data: raw}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	if keep {
		h.seq++
		ev.ID = h.epoch + "-" + strconv.FormatUint(h.seq, 10)
		h.ring = append(h.ring, ev)
		if len(h.ring) > h.backlog {
			h.ring = append(h.ring[:0], h.ring[len(h.ring)-h.backlog:]...)
		}
	}
	for s := range h.subs {
		select {
		case s.ch <- ev:
		default:
			// Too slow: drop it; it reconnects with its last id.
			delete(h.subs, s)
			s.drop()
		}
	}
}

// Subscribers is the number of connected clients.
func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// OnSubscribers registers f to be told the number of subscribers whenever
// it changes by a subscribe or close.
func (h *Hub) OnSubscribers(f func(int)) {
	h.mu.Lock()
	h.watchers = append(h.watchers, f)
	h.mu.Unlock()
}

// Close drops every subscriber; later publishes do nothing.
func (h *Hub) Close() {
	h.mu.Lock()
	h.closed = true
	subs := h.subs
	h.subs = map[*Subscription]struct{}{}
	h.mu.Unlock()
	for s := range subs {
		s.drop()
	}
}
