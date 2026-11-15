// Package notes keeps short notes left on sessions: instructions for the
// agents (and people) that watch a session, such as "answer the review on
// its PR" or "archive it once the PR is merged". They are not sent to the
// session's own agent. The gateway keeps them in its state dir, so every
// client sees the same notes.
package notes

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// MaxText is the longest note, in bytes.
const MaxText = 8 << 10

// MaxPerSession bounds how many notes one session keeps.
const MaxPerSession = 50

var (
	ErrNotFound = errors.New("note not found")
	ErrInvalid  = errors.New("invalid note")
)

type Store struct {
	path string
	now  func() time.Time

	mu    sync.Mutex
	notes map[string]model.Note // note id -> note
}

type file struct {
	Notes []model.Note `json:"notes"`
}

// Open loads the store; a missing file is an empty store.
func Open(path string) (*Store, error) {
	s := &Store{path: path, now: time.Now, notes: map[string]model.Note{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	for _, n := range f.Notes {
		s.notes[n.ID] = n
	}
	return s, nil
}

// Change is what Create and Update take. A nil field is left as it is; an
// empty ExpiresAt (zero time) removes the expiry.
type Change struct {
	Text      *string
	Author    string
	ExpiresAt *time.Time
}

func cleanText(t string) (string, error) {
	t = strings.TrimSpace(t)
	if t == "" {
		return "", errors.Join(ErrInvalid, errors.New("text is empty"))
	}
	if len(t) > MaxText {
		return "", errors.Join(ErrInvalid, errors.New("text is longer than 8 KiB"))
	}
	return t, nil
}

// Create adds a note to a session.
func (s *Store) Create(sessionID string, c Change) (model.Note, error) {
	if sessionID == "" || c.Text == nil {
		return model.Note{}, errors.Join(ErrInvalid, errors.New("session and text are required"))
	}
	text, err := cleanText(*c.Text)
	if err != nil {
		return model.Note{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	s.pruneLocked(now)
	if len(s.forLocked(sessionID)) >= MaxPerSession {
		return model.Note{}, errors.Join(ErrInvalid, errors.New("this session already has 50 notes"))
	}
	n := model.Note{ID: s.newIDLocked(), SessionID: sessionID, Text: text, Author: c.Author, CreatedAt: now, UpdatedAt: now}
	if c.ExpiresAt != nil && !c.ExpiresAt.IsZero() {
		if !c.ExpiresAt.After(now) {
			return model.Note{}, errors.Join(ErrInvalid, errors.New("expiry is in the past"))
		}
		t := c.ExpiresAt.UTC()
		n.ExpiresAt = &t
	}
	s.notes[n.ID] = n
	if err := s.saveLocked(); err != nil {
		delete(s.notes, n.ID)
		return model.Note{}, err
	}
	return n, nil
}

// Update changes a note's text or expiry. Author, when given, records who
// changed it last.
func (s *Store) Update(id string, c Change) (model.Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	s.pruneLocked(now)
	n, ok := s.notes[id]
	if !ok {
		return model.Note{}, ErrNotFound
	}
	old := n
	if c.Text != nil {
		text, err := cleanText(*c.Text)
		if err != nil {
			return model.Note{}, err
		}
		n.Text = text
	}
	if c.ExpiresAt != nil {
		if c.ExpiresAt.IsZero() {
			n.ExpiresAt = nil
		} else if !c.ExpiresAt.After(now) {
			return model.Note{}, errors.Join(ErrInvalid, errors.New("expiry is in the past"))
		} else {
			t := c.ExpiresAt.UTC()
			n.ExpiresAt = &t
		}
	}
	if c.Author != "" {
		n.UpdatedBy = c.Author
	}
	n.UpdatedAt = now
	s.notes[id] = n
	if err := s.saveLocked(); err != nil {
		s.notes[id] = old
		return model.Note{}, err
	}
	return n, nil
}

// Get returns one note.
func (s *Store) Get(id string) (model.Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now().UTC())
	n, ok := s.notes[id]
	if !ok {
		return model.Note{}, ErrNotFound
	}
	return n, nil
}

// Delete removes one note.
func (s *Store) Delete(id string) (model.Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now().UTC())
	n, ok := s.notes[id]
	if !ok {
		return model.Note{}, ErrNotFound
	}
	delete(s.notes, id)
	if err := s.saveLocked(); err != nil {
		s.notes[id] = n
		return model.Note{}, err
	}
	return n, nil
}

// Clear removes every note of a session and returns them.
func (s *Store) Clear(sessionID string) ([]model.Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now().UTC())
	gone := s.forLocked(sessionID)
	if len(gone) == 0 {
		return nil, nil
	}
	for _, n := range gone {
		delete(s.notes, n.ID)
	}
	if err := s.saveLocked(); err != nil {
		for _, n := range gone {
			s.notes[n.ID] = n
		}
		return nil, err
	}
	return gone, nil
}

// For returns a session's notes, oldest first.
func (s *Store) For(sessionID string) []model.Note {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now().UTC())
	return s.forLocked(sessionID)
}

// All returns every note, grouped by session and oldest first within one.
func (s *Store) All() []model.Note {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now().UTC())
	out := make([]model.Note, 0, len(s.notes))
	for _, n := range s.notes {
		out = append(out, n)
	}
	sortNotes(out)
	return out
}

func (s *Store) forLocked(sessionID string) []model.Note {
	var out []model.Note
	for _, n := range s.notes {
		if n.SessionID == sessionID {
			out = append(out, n)
		}
	}
	sortNotes(out)
	return out
}

func sortNotes(ns []model.Note) {
	sort.Slice(ns, func(i, j int) bool {
		if ns[i].SessionID != ns[j].SessionID {
			return ns[i].SessionID < ns[j].SessionID
		}
		if !ns[i].CreatedAt.Equal(ns[j].CreatedAt) {
			return ns[i].CreatedAt.Before(ns[j].CreatedAt)
		}
		return ns[i].ID < ns[j].ID
	})
}

// pruneLocked forgets expired notes. The file is rewritten with the next
// change; a reload before that drops them again.
func (s *Store) pruneLocked(now time.Time) {
	for id, n := range s.notes {
		if n.ExpiresAt != nil && !n.ExpiresAt.After(now) {
			delete(s.notes, id)
		}
	}
}

func (s *Store) newIDLocked() string {
	for {
		var b [4]byte
		rand.Read(b[:])
		id := "n" + hex.EncodeToString(b[:])
		if _, taken := s.notes[id]; !taken {
			return id
		}
	}
}

func (s *Store) saveLocked() error {
	list := make([]model.Note, 0, len(s.notes))
	for _, n := range s.notes {
		list = append(list, n)
	}
	sortNotes(list)
	b, err := json.MarshalIndent(file{Notes: list}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
