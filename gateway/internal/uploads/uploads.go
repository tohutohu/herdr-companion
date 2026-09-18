// Package uploads stores files sent from Android as temporary files.
// Each upload gets its own directory named by the upload id, so the original
// file name survives and the path handed to the agent stays meaningful.
package uploads

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const MaxUploadSize = 20 << 20

var (
	ErrTooLarge = errors.New("upload too large")
	ErrNotFound = errors.New("upload not found")
)

// Extensions used when the client sends no usable file name.
var extensions = map[string]string{
	"image/png":        ".png",
	"image/jpeg":       ".jpg",
	"image/gif":        ".gif",
	"image/webp":       ".webp",
	"text/plain":       ".txt",
	"application/pdf":  ".pdf",
	"application/zip":  ".zip",
	"application/json": ".json",
}

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// File is one stored upload.
type File struct {
	ID   string
	Name string
	Path string
	Mime string
}

// IsImage reports whether the file can be attached to an agent as an image.
func (f File) IsImage() bool { return strings.HasPrefix(f.Mime, "image/") }

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

// Save writes the body to a new upload directory. name is the client's file
// name and may be empty, in which case one is derived from contentType.
func (s *Store) Save(r io.Reader, contentType, name string) (File, error) {
	b := make([]byte, 16)
	rand.Read(b)
	id := hex.EncodeToString(b)
	name = safeName(name)
	if name == "" {
		name = id + extensionFor(contentType)
	}
	dir := filepath.Join(s.dir, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return File{}, err
	}
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		os.RemoveAll(dir)
		return File{}, err
	}
	n, err := io.Copy(f, io.LimitReader(r, MaxUploadSize+1))
	f.Close()
	if err == nil && n > MaxUploadSize {
		err = ErrTooLarge
	}
	if err != nil {
		os.RemoveAll(dir)
		return File{}, err
	}
	mt := mimeFromExt(name)
	if mt == "" {
		mt = parseMedia(contentType)
	}
	if mt == "" {
		mt = "application/octet-stream"
	}
	return File{ID: id, Name: name, Path: path, Mime: mt}, nil
}

// Get returns the stored file for an upload id.
func (s *Store) Get(id string) (File, error) {
	if !idPattern.MatchString(id) {
		return File{}, ErrNotFound
	}
	dir := filepath.Join(s.dir, id)
	des, err := os.ReadDir(dir)
	if err != nil {
		return File{}, ErrNotFound
	}
	for _, de := range des {
		if de.IsDir() {
			continue
		}
		path := filepath.Join(dir, de.Name())
		mt := mimeFromExt(de.Name())
		if mt == "" {
			mt = sniff(path)
		}
		return File{ID: id, Name: de.Name(), Path: path, Mime: mt}, nil
	}
	return File{}, ErrNotFound
}

// safeName reduces a client file name to a single path element that is safe to
// join onto the upload directory. Whitespace becomes "_" so the path stays one
// word when it is pasted into an agent prompt.
func safeName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == '\\' || r == 0 || unicode.IsControl(r):
			// dropped
		case unicode.IsSpace(r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	name = strings.TrimLeft(b.String(), ".")
	if len(name) > 120 {
		ext := filepath.Ext(name)
		if len(ext) > 16 {
			ext = ""
		}
		name = name[:120-len(ext)] + ext
	}
	return name
}

func extensionFor(contentType string) string {
	mt, _, _ := strings.Cut(contentType, ";")
	if ext, ok := extensions[strings.TrimSpace(strings.ToLower(mt))]; ok {
		return ext
	}
	return ".bin"
}

func mimeFromExt(name string) string { return parseMedia(mime.TypeByExtension(filepath.Ext(name))) }

func parseMedia(s string) string {
	mt, _, err := mime.ParseMediaType(s)
	if err != nil {
		return ""
	}
	return mt
}

// sniff types a file whose name carries no extension. Nothing but the stored
// bytes is kept, so the Content-Type the client sent is gone by then.
func sniff(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	if mt := parseMedia(http.DetectContentType(head[:n])); mt != "" {
		return mt
	}
	return "application/octet-stream"
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
		if err := os.RemoveAll(filepath.Join(s.dir, de.Name())); err == nil {
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
