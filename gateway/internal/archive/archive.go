// Package archive remembers which sessions the user archived. Providers have
// no common archive concept, so the gateway keeps the ids in its state dir.
package archive

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Store struct {
	path string

	mu       sync.Mutex
	sessions map[string]time.Time // gateway session id -> archived at
}

type file struct {
	Sessions map[string]time.Time `json:"sessions"`
}

// Open loads the store; a missing file is an empty store.
func Open(path string) (*Store, error) {
	s := &Store{path: path, sessions: map[string]time.Time{}}
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
	if f.Sessions != nil {
		s.sessions = f.Sessions
	}
	return s, nil
}

func (s *Store) Has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.sessions[id]
	return ok
}

// IDs returns archived ids, most recently archived first.
func (s *Store) IDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return s.sessions[ids[i]].After(s.sessions[ids[j]]) })
	return ids
}

func (s *Store) Add(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[id]; ok {
		return nil
	}
	s.sessions[id] = time.Now().UTC()
	return s.saveLocked()
}

func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[id]; !ok {
		return nil
	}
	delete(s.sessions, id)
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(file{Sessions: s.sessions}, "", "  ")
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
