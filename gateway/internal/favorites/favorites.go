// Package favorites keeps the user's favorite working folders, in the order
// the user put them. The gateway stores them in its state dir, so the apps
// and the agents driving the gateway (hc) all pick a project from the same
// list. Each favorite is also a project (docs/api.md#プロジェクト): sessions and
// resident tasks under it belong to it, and it has a display name and a
// notification level.
package favorites

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"
)

// MaxCount bounds the list.
const MaxCount = 200

// MaxPathLen is the longest path kept, in bytes.
const MaxPathLen = 4096

var ErrInvalid = errors.New("invalid favorite folder")

// ErrNotFound: the folder is not a favorite.
var ErrNotFound = errors.New("not a favorite folder")

// Notification levels of a project.
const (
	// NotifyAll pushes every transition, as without projects.
	NotifyAll = "all"
	// NotifyAttention pushes only what needs the user: questions,
	// approvals and failures.
	NotifyAttention = "attention"
	// NotifyOff pushes nothing.
	NotifyOff = "off"
)

// MaxNameLen bounds a project's display name, in bytes.
const MaxNameLen = 200

// Settings are what the user set for one project; empty fields are defaults.
type Settings struct {
	Name   string `json:"name,omitempty"`
	Notify string `json:"notify,omitempty"`
}

// Project is a favorite folder with its settings filled in.
type Project struct {
	Path string `json:"path"`
	// Name defaults to the folder's base name.
	Name   string `json:"name"`
	Notify string `json:"notify"`
}

func project(path string, set Settings) Project {
	p := Project{Path: path, Name: set.Name, Notify: set.Notify}
	if p.Name == "" {
		p.Name = filepath.Base(path)
	}
	if p.Notify == "" {
		p.Notify = NotifyAll
	}
	return p
}

// ValidNotify reports whether level is a notification level.
func ValidNotify(level string) bool {
	return level == NotifyAll || level == NotifyAttention || level == NotifyOff
}

type Store struct {
	path string

	mu    sync.Mutex
	paths []string
	// settings are kept for folders in paths only.
	settings map[string]Settings
	// saved is false until the list is first written. Clients use it to move
	// the favorites they kept on the device to the gateway once.
	saved bool
}

type file struct {
	Paths    []string            `json:"paths"`
	Projects map[string]Settings `json:"projects,omitempty"`
}

// Open loads the store; a missing file is an empty store that was never saved.
func Open(path string) (*Store, error) {
	s := &Store{path: path, settings: map[string]Settings{}}
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
	for p, set := range f.Projects {
		c, err := clean(p)
		if err != nil || !contains(s.paths, c) {
			continue
		}
		if !ValidNotify(set.Notify) {
			set.Notify = ""
		}
		s.settings[c] = set
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

// Projects returns the favorites as projects, in order.
func (s *Store) Projects() []Project {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Project, 0, len(s.paths))
	for _, p := range s.paths {
		out = append(out, project(p, s.settings[p]))
	}
	return out
}

// Update changes a project's name and notification level; nil leaves one as
// it is and an empty name returns to the default.
func (s *Store) Update(path string, name, notify *string) (Project, error) {
	p, err := clean(path)
	if err != nil {
		return Project{}, err
	}
	if notify != nil && *notify != "" && !ValidNotify(*notify) {
		return Project{}, errors.Join(ErrInvalid, fmt.Errorf("notify must be %s, %s or %s", NotifyAll, NotifyAttention, NotifyOff))
	}
	if name != nil && len(*name) > MaxNameLen {
		return Project{}, errors.Join(ErrInvalid, errors.New("name is too long"))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !contains(s.paths, p) {
		return Project{}, ErrNotFound
	}
	set := s.settings[p]
	if name != nil {
		set.Name = *name
	}
	if notify != nil {
		set.Notify = *notify
		if set.Notify == NotifyAll {
			set.Notify = ""
		}
	}
	next := maps.Clone(s.settings)
	if set == (Settings{}) {
		delete(next, p)
	} else {
		next[p] = set
	}
	if err := s.write(s.paths, next); err != nil {
		return Project{}, err
	}
	s.settings = next
	s.saved = true
	return project(p, set), nil
}

func (s *Store) snapshotLocked() Snapshot {
	return Snapshot{Paths: append([]string{}, s.paths...), Saved: s.saved}
}

func (s *Store) replaceLocked(list []string) (Snapshot, error) {
	// A folder taken out of the list loses its settings.
	next := map[string]Settings{}
	for _, p := range list {
		if set, ok := s.settings[p]; ok {
			next[p] = set
		}
	}
	if err := s.write(list, next); err != nil {
		return Snapshot{}, err
	}
	s.paths = list
	s.settings = next
	s.saved = true
	return s.snapshotLocked(), nil
}

func (s *Store) write(list []string, settings map[string]Settings) error {
	f := file{Paths: append([]string{}, list...)}
	if len(settings) > 0 {
		f.Projects = settings
	}
	b, err := json.MarshalIndent(f, "", "  ")
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
