// Package favorites keeps the user's favorite working folders, in the order
// the user put them. The gateway stores them in its state dir, so the apps
// and the agents driving the gateway (hc) all pick a project from the same
// list.
package favorites

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// MaxCount bounds the list.
const MaxCount = 200

// MaxPathLen is the longest path kept, in bytes.
const MaxPathLen = 4096

var ErrInvalid = errors.New("invalid favorite folder")

type Store struct {
	path string

	mu    sync.Mutex
	paths []string
	// saved is false until the list is first written. Clients use it to move
	// the favorites they kept on the device to the gateway once.
	saved bool
}

type file struct {
	Paths []string `json:"paths"`
}

// Open loads the store; a missing file is an empty store that was never saved.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
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
	// A hand-edited file is cleaned as a client's list would be, dropping what is wrong.
	for _, p := range f.Paths {
		if c, err := clean(p); err == nil && !contains(s.paths, c) {
			s.paths = append(s.paths, c)
		}
	}
	s.saved = true
	return s, nil
}

// Snapshot is the list and whether it was ever saved.
type Snapshot struct {
	Paths []string `json:"paths"`
	Saved bool     `json:"saved"`
}

// List returns the favorites in order.
func (s *Store) List() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// Set replaces the list (a reorder, or a client's whole edited list).
func (s *Store) Set(paths []string) (Snapshot, error) {
	list, err := cleanList(paths)
	if err != nil {
		return Snapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.replaceLocked(list)
}

// Import sets the list only if it was never saved, so the first client to
// move its own favorites to the gateway wins and later ones keep the
// gateway's list. imported tells which happened.
func (s *Store) Import(paths []string) (snap Snapshot, imported bool, err error) {
	list, err := cleanList(paths)
	if err != nil {
		return Snapshot{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saved {
		return s.snapshotLocked(), false, nil
	}
	snap, err = s.replaceLocked(list)
	return snap, err == nil, err
}

// Add puts path at position (0-based; out of range or negative appends).
// A folder already in the list moves there only when position is given.
func (s *Store) Add(path string, position *int) (Snapshot, error) {
	p, err := clean(path)
	if err != nil {
		return Snapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if contains(s.paths, p) && position == nil {
		return s.snapshotLocked(), nil
	}
	list := without(s.paths, p)
	if len(list) >= MaxCount {
		return Snapshot{}, errors.Join(ErrInvalid, fmt.Errorf("at most %d favorites", MaxCount))
	}
	at := len(list)
	if position != nil && *position >= 0 && *position < len(list) {
		at = *position
	}
	list = append(list[:at:at], append([]string{p}, list[at:]...)...)
	return s.replaceLocked(list)
}

// Remove takes path out of the list; one that is not in it is no error.
func (s *Store) Remove(path string) (Snapshot, error) {
	p, err := clean(path)
	if err != nil {
		return Snapshot{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !contains(s.paths, p) {
		return s.snapshotLocked(), nil
	}
	return s.replaceLocked(without(s.paths, p))
}

func (s *Store) snapshotLocked() Snapshot {
	return Snapshot{Paths: append([]string{}, s.paths...), Saved: s.saved}
}

func (s *Store) replaceLocked(list []string) (Snapshot, error) {
	if err := s.write(list); err != nil {
		return Snapshot{}, err
	}
	s.paths = list
	s.saved = true
	return s.snapshotLocked(), nil
}

func (s *Store) write(list []string) error {
	b, err := json.MarshalIndent(file{Paths: append([]string{}, list...)}, "", "  ")
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

func clean(p string) (string, error) {
	if p == "" || !filepath.IsAbs(p) {
		return "", errors.Join(ErrInvalid, fmt.Errorf("%q is not an absolute path", p))
	}
	if len(p) > MaxPathLen {
		return "", errors.Join(ErrInvalid, errors.New("path is too long"))
	}
	return filepath.Clean(p), nil
}

// cleanList cleans every path and drops repeats, keeping the first.
func cleanList(paths []string) ([]string, error) {
	out := []string{}
	for _, p := range paths {
		c, err := clean(p)
		if err != nil {
			return nil, err
		}
		if !contains(out, c) {
			out = append(out, c)
		}
	}
	if len(out) > MaxCount {
		return nil, errors.Join(ErrInvalid, fmt.Errorf("at most %d favorites", MaxCount))
	}
	return out, nil
}

func contains(list []string, p string) bool {
	for _, x := range list {
		if x == p {
			return true
		}
	}
	return false
}

func without(list []string, p string) []string {
	out := make([]string, 0, len(list))
	for _, x := range list {
		if x != p {
			out = append(out, x)
		}
	}
	return out
}
