// Package deadletter records data the gateway could not process, together with
// the raw payload, as JSONL files (one file per day). This is the only
// diagnostic data the gateway persists.
package deadletter

import (
	"crypto/sha256"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Kind string

const (
	UnknownEvent           Kind = "unknown_event"
	UnknownContent         Kind = "unknown_content"
	UnsupportedInteraction Kind = "unsupported_interaction"
	ParseError             Kind = "parse_error"
	ProviderError          Kind = "provider_error"
	FileError              Kind = "file_error"
	SendError              Kind = "send_error"
)

type Entry struct {
	Timestamp time.Time       `json:"timestamp"`
	Provider  string          `json:"provider,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Kind      Kind            `json:"kind"`
	Error     string          `json:"error"`
	Raw       json.RawMessage `json:"raw,omitempty"`
}

// Sink is what parsers depend on. Tests can use a Recorder.
type Sink interface {
	Record(provider, sessionID string, kind Kind, errMsg string, raw any)
}

// Writer appends entries to <dir>/<YYYY-MM-DD>.jsonl. Transcripts are parsed
// again on every poll, so identical entries are de-duplicated in memory to
// keep the log readable.
type Writer struct {
	dir  string
	mu   sync.Mutex
	seen map[[32]byte]struct{}
	now  func() time.Time
}

const maxSeen = 10000

func NewWriter(dir string) *Writer {
	return &Writer{dir: dir, seen: map[[32]byte]struct{}{}, now: time.Now}
}

func (w *Writer) Record(provider, sessionID string, kind Kind, errMsg string, raw any) {
	rawJSON := toRaw(raw)
	key := sha256.Sum256([]byte(provider + "\x00" + sessionID + "\x00" + string(kind) + "\x00" + errMsg + "\x00" + string(rawJSON)))

	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.seen[key]; ok {
		return
	}
	if len(w.seen) >= maxSeen {
		w.seen = map[[32]byte]struct{}{}
	}
	w.seen[key] = struct{}{}

	e := Entry{Timestamp: w.now().UTC(), Provider: provider, SessionID: sessionID, Kind: kind, Error: errMsg, Raw: rawJSON}
	line, err := json.Marshal(e)
	if err != nil {
		slog.Error("dead-letter marshal failed", "error", err)
		return
	}
	if err := os.MkdirAll(w.dir, 0o700); err != nil {
		slog.Error("dead-letter mkdir failed", "error", err)
		return
	}
	name := filepath.Join(w.dir, e.Timestamp.Format("2006-01-02")+".jsonl")
	f, err := os.OpenFile(name, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		slog.Error("dead-letter open failed", "error", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		slog.Error("dead-letter write failed", "error", err)
	}
	slog.Warn("dead-letter recorded", "provider", provider, "session_id", sessionID, "kind", kind, "error", errMsg)
}

func toRaw(raw any) json.RawMessage {
	switch v := raw.(type) {
	case nil:
		return nil
	case json.RawMessage:
		if json.Valid(v) {
			return v
		}
		b, _ := json.Marshal(string(v))
		return b
	case []byte:
		if json.Valid(v) {
			return json.RawMessage(v)
		}
		b, _ := json.Marshal(string(v))
		return b
	default:
		b, err := json.Marshal(v)
		if err != nil {
			b, _ = json.Marshal(err.Error())
		}
		return b
	}
}

// Recorder keeps entries in memory (tests, replay).
type Recorder struct {
	mu      sync.Mutex
	Entries []Entry
}

func (r *Recorder) Record(provider, sessionID string, kind Kind, errMsg string, raw any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Entries = append(r.Entries, Entry{Provider: provider, SessionID: sessionID, Kind: kind, Error: errMsg, Raw: toRaw(raw)})
}

// Nop discards entries.
type Nop struct{}

func (Nop) Record(string, string, Kind, string, any) {}
