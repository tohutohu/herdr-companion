package providers

import (
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

func itoa(i int) string { return strconv.Itoa(i) }

func queryEscape(s string) string { return url.QueryEscape(s) }

// ProjectName derives a short project label from a working directory.
func ProjectName(cwd string) string {
	if cwd == "" {
		return ""
	}
	return filepath.Base(filepath.Clean(cwd))
}

// OneLine collapses whitespace and truncates for list previews.
func OneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func IsImagePath(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		return true
	}
	return false
}

// TextWithFiles returns the prompt text with every non-image attachment
// appended as its own line. Agents have no structured form for these, so the
// path is all they get; they read the file themselves.
func TextWithFiles(in model.Input) string {
	text := strings.TrimSpace(in.Text)
	for _, f := range in.Files {
		text = strings.TrimSpace(text + "\n" + f)
	}
	return text
}
