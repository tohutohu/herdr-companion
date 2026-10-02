package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// stateStore keeps what each reader (cursor) has already seen. Files are
// small and written whole through a rename, and each session's read mark
// has its own file, so a "wait" running in the background and reads in the
// foreground never overwrite each other.
type stateStore struct{ dir string }

func stateDir() string {
	if d := os.Getenv("HC_STATE_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "herdr-mobile", "hc")
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "herdr-mobile", "hc")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "herdr-mobile", "hc")
}

// snapshot is the session list as the reader last saw it.
type snapshot struct {
	TakenAt  time.Time              `json:"takenAt"`
	Sessions map[string]seenSession `json:"sessions"`
}

type seenSession struct {
	Status      model.Status `json:"status"`
	UpdatedAt   time.Time    `json:"updatedAt"`
	LastMessage string       `json:"lastMessage,omitempty"`
}

// readMark is the last message of a session the reader was shown.
type readMark struct {
	LastID string    `json:"lastId"`
	Hash   string    `json:"hash"`
	At     time.Time `json:"at"`
}

func (s *stateStore) snapshotPath(cursor string) string {
	return filepath.Join(s.dir, safeName(cursor), "sessions.json")
}

func (s *stateStore) readPath(cursor, session string) string {
	return filepath.Join(s.dir, safeName(cursor), "reads", safeName(session)+".json")
}

// loadSnapshot returns nil when the reader has no snapshot yet.
func (s *stateStore) loadSnapshot(cursor string) (*snapshot, error) {
	var snap snapshot
	ok, err := readJSON(s.snapshotPath(cursor), &snap)
	if !ok || err != nil {
		return nil, err
	}
	if snap.Sessions == nil {
		snap.Sessions = map[string]seenSession{}
	}
	return &snap, nil
}

func (s *stateStore) saveSnapshot(cursor string, sessions []model.Session, now time.Time) error {
	return s.writeSnapshot(cursor, sessions, now)
}

func (s *stateStore) writeSnapshot(cursor string, sessions []model.Session, takenAt time.Time) error {
	snap := snapshot{TakenAt: takenAt, Sessions: map[string]seenSession{}}
	for _, sess := range sessions {
		snap.Sessions[sess.ID] = seen(sess)
	}
	return writeJSON(s.snapshotPath(cursor), snap)
}

func seen(s model.Session) seenSession {
	return seenSession{Status: s.Status, UpdatedAt: s.UpdatedAt, LastMessage: s.LastMessage}
}

func (s *stateStore) loadRead(cursor, session string) (*readMark, error) {
	var m readMark
	ok, err := readJSON(s.readPath(cursor, session), &m)
	if !ok || err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *stateStore) saveRead(cursor, session string, m readMark) error {
	return writeJSON(s.readPath(cursor, session), m)
}

func readJSON(path string, v any) (bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(b, v); err != nil {
		// A damaged cursor only costs a re-read; start over.
		return false, nil
	}
	return true, nil
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// safeName makes a cursor or session id usable as one path element.
func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, s)
}
