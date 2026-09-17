package model

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// fileRefPattern matches things like src/auth.go, ./a/b.kt:42, /abs/path/x.md:10:3
// The left boundary is any non-path character so references directly after
// Japanese punctuation (e.g. "。src/a.go") are found too.
var fileRefPattern = regexp.MustCompile(`(?:^|[^\w./@+-])((?:\.{0,2}/)?(?:[\w.@+-]+/)*[\w@+-][\w.@+-]*\.[A-Za-z0-9]{1,10})(?::(\d+))?`)

const maxFileRefs = 20

// ExtractFileRefs finds file references in text that exist under root.
// Paths are returned relative to root. Non-existing candidates are dropped so
// that "e.g." or version numbers do not become links.
func ExtractFileRefs(text, root string) []Block {
	if root == "" || text == "" {
		return nil
	}
	var out []Block
	seen := map[string]bool{}
	for _, m := range fileRefPattern.FindAllStringSubmatch(text, -1) {
		p := m[1]
		if strings.Contains(p, "://") || strings.HasPrefix(p, "www.") {
			continue
		}
		line, _ := strconv.Atoi(m[2])
		b, ok := FileRef(root, p, line)
		if !ok {
			continue
		}
		key := b.Path + ":" + m[2]
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, b)
		if len(out) >= maxFileRefs {
			break
		}
	}
	return out
}

// FileRef resolves p against root and returns a file block with the
// root-relative path when p names an existing regular file inside root. This
// is only a hint for link generation; access control happens in the files
// package.
func FileRef(root, p string, line int) (Block, bool) {
	abs := p
	if !filepath.IsAbs(p) {
		abs = filepath.Join(root, p)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return Block{}, false
	}
	st, err := os.Stat(abs)
	if err != nil || !st.Mode().IsRegular() {
		return Block{}, false
	}
	return Block{Type: BlockFile, Path: rel, Line: line, Size: st.Size()}, true
}

// Truncate shortens s to at most n runes, marking the cut.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "\n… (truncated)"
}

// DisplayPath returns p relative to root when it lies inside root.
func DisplayPath(root, p string) string {
	if root == "" || !filepath.IsAbs(p) {
		return p
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return p
	}
	return rel
}
