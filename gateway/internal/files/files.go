// Package files gives read-only access to files under a session's workspace
// root. Every path is cleaned, symlink-resolved and checked to stay inside an
// allowed root.
package files

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	ErrForbidden = errors.New("path is outside the workspace")
	ErrNotFound  = errors.New("file not found")
	ErrTooLarge  = errors.New("file too large")
	ErrNoRoot    = errors.New("session has no workspace root")
)

// MaxFileSize limits files returned for in-app preview. Downloads are unlimited.
const MaxFileSize = 10 << 20

// Names that are never served even inside a workspace.
var deniedNames = map[string]bool{".ssh": true, ".gnupg": true, ".aws": true}

// Resolve returns the real absolute path of p (relative to the first root, or
// absolute) if it exists inside one of roots.
func Resolve(roots []string, p string) (string, error) {
	if len(roots) == 0 || roots[0] == "" {
		return "", ErrNoRoot
	}
	if strings.ContainsRune(p, 0) {
		return "", ErrForbidden
	}
	candidate := p
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(roots[0], candidate)
	}
	candidate = filepath.Clean(candidate)
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNotFound
		}
		return "", err
	}
	for _, root := range roots {
		if root == "" || !filepath.IsAbs(root) {
			continue
		}
		realRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
		if err != nil {
			continue
		}
		if within(realRoot, real) {
			for _, part := range strings.Split(real, string(filepath.Separator)) {
				if deniedNames[part] {
					return "", ErrForbidden
				}
			}
			return real, nil
		}
	}
	return "", ErrForbidden
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

type Entry struct {
	Name  string `json:"name"`
	Path  string `json:"path"` // relative to the workspace root
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
}

// List lists a directory inside the workspace.
func List(roots []string, p string) ([]Entry, error) {
	if p == "" {
		p = "."
	}
	dir, err := Resolve(roots, p)
	if err != nil {
		return nil, err
	}
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	realRoot, _ := filepath.EvalSymlinks(roots[0])
	var out []Entry
	for _, de := range des {
		if deniedNames[de.Name()] || de.Name() == ".git" {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(realRoot, filepath.Join(dir, de.Name()))
		out = append(out, Entry{Name: de.Name(), Path: rel, IsDir: de.IsDir(), Size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Open opens a regular file inside the workspace and detects its content type.
// maxSize <= 0 means no limit.
func Open(roots []string, p string, maxSize int64) (f *os.File, size int64, contentType string, err error) {
	real, err := Resolve(roots, p)
	if err != nil {
		return nil, 0, "", err
	}
	st, err := os.Stat(real)
	if err != nil {
		return nil, 0, "", err
	}
	if !st.Mode().IsRegular() {
		return nil, 0, "", ErrNotFound
	}
	if maxSize > 0 && st.Size() > maxSize {
		return nil, 0, "", ErrTooLarge
	}
	f, err = os.Open(real)
	if err != nil {
		return nil, 0, "", err
	}
	contentType = mime.TypeByExtension(filepath.Ext(real))
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	f.Seek(0, io.SeekStart)
	sniffed := http.DetectContentType(head[:n])
	// Text of any kind (html, json, source) is served as plain text so the
	// client never renders active content.
	switch {
	case strings.HasPrefix(contentType, "image/"):
	case strings.HasPrefix(sniffed, "text/"):
		contentType = "text/plain; charset=utf-8"
	case sniffed != "application/octet-stream":
		contentType = sniffed
	case looksText(head[:n]):
		contentType = "text/plain; charset=utf-8"
	default:
		contentType = "application/octet-stream"
	}
	return f, st.Size(), contentType, nil
}

func looksText(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return false
		}
	}
	return true
}

type Info struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ContentType string `json:"contentType"`
	// Previewable is true when the app can show the file inline.
	Previewable bool `json:"previewable"`
}

// Stat describes a file inside the workspace without returning its content.
func Stat(roots []string, p string) (*Info, error) {
	f, size, ctype, err := Open(roots, p, 0)
	if err != nil {
		return nil, err
	}
	f.Close()
	previewable := size <= MaxFileSize && (strings.HasPrefix(ctype, "image/") || strings.HasPrefix(ctype, "text/"))
	return &Info{Path: p, Name: filepath.Base(f.Name()), Size: size, ContentType: ctype, Previewable: previewable}, nil
}
