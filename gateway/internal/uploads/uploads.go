// Package uploads stores images sent from Android as temporary files.
// The file name is the upload id; nothing else is tracked.
package uploads

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const MaxUploadSize = 20 << 20

var (
	ErrUnsupportedType = errors.New("only png, jpeg, gif and webp images are accepted")
	ErrTooLarge        = errors.New("upload too large")
	ErrNotFound        = errors.New("upload not found")
)

var extensions = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type Store struct {
	dir string
	ttl time.Duration
}

func DefaultDir() string { return filepath.Join(os.TempDir(), "herdr-mobile", "uploads") }

func New(dir string, ttl time.Duration) *Store {
	if dir == "" {
		dir = DefaultDir()
	}
	return &Store{dir: dir, ttl: ttl}
}

func (s *Store) Dir() string { return s.dir }

func (s *Store) Save(r io.Reader, contentType string) (string, error) {
	mt, _, _ := strings.Cut(contentType, ";")
	ext, ok := extensions[strings.TrimSpace(strings.ToLower(mt))]
	if !ok {
		return "", ErrUnsupportedType
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", err
	}
	b := make([]byte, 16)
	rand.Read(b)
	id := hex.EncodeToString(b)
	path := filepath.Join(s.dir, id+ext)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(r, MaxUploadSize+1))
	f.Close()
	if err == nil && n > MaxUploadSize {
		err = ErrTooLarge
	}
	if err != nil {
		os.Remove(path)
		return "", err
	}
	return id, nil
}

// Path returns the absolute file path for an upload id.
func (s *Store) Path(id string) (string, error) {
	if !idPattern.MatchString(id) {
		return "", ErrNotFound
	}
	for _, ext := range extensions {
		p := filepath.Join(s.dir, id+ext)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", ErrNotFound
}

// Cleanup removes uploads older than the TTL.
func (s *Store) Cleanup() {
	des, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-s.ttl)
	for _, de := range des {
		info, err := de.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(s.dir, de.Name())); err == nil {
			slog.Debug("removed expired upload", "operation", "upload.cleanup", "file", de.Name())
		}
	}
}

func (s *Store) RunCleanup(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		s.Cleanup()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
