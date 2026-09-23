package claude

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// Claude Code records the working directory on every transcript line, and it
// changes when the agent cd's around. A path in a reply is written relative to
// the directory at that moment, not to where the session is now, so each
// entry's file references are looked up from its own cwd.

// refKey identifies one file-reference lookup: text is searched from root.
type refKey struct{ root, text string }

// refRoot is the directory an entry's relative paths point into: its recorded
// cwd while that still exists, otherwise the session's current root (a moved
// or deleted directory). Without a current root no references are made, as
// in summaries. seen memoizes the directory check within one conversion.
func refRoot(cwd, current string, seen map[string]string) string {
	if current == "" || cwd == "" || cwd == current {
		return current
	}
	if r, ok := seen[cwd]; ok {
		return r
	}
	r := current
	if st, err := os.Stat(cwd); err == nil && st.IsDir() {
		r = cwd
	}
	seen[cwd] = r
	return r
}

// fileRefs returns the files text refers to, found from the entry's root and,
// for absolute paths or an entry written elsewhere, from the current root.
// Paths are made usable by the files API, which resolves relative paths
// against the current root.
//
// Lookups are cached per transcript: every poll converts the whole transcript
// again, and without the cache each reply's candidates would be stat'ed each
// time. A file created after the reply therefore gets no link, and a deleted
// one keeps its link (the files API then answers not found, as for any stale
// reference).
func (t *Transcript) fileRefs(text, root, current string) []model.Block {
	if root == "" {
		return nil
	}
	refs := t.lookupRefs(text, root)
	if root != current {
		refs = append(rebase(refs, root, current), t.lookupRefs(text, current)...)
	}
	return dedupeRefs(refs)
}

func (t *Transcript) lookupRefs(text, root string) []model.Block {
	if t.refs == nil {
		t.refs = map[refKey][]model.Block{}
	}
	k := refKey{root, text}
	refs, ok := t.refs[k]
	if !ok {
		refs = model.ExtractFileRefs(text, root)
		t.refs[k] = refs
	}
	// The cached slice is shared by every conversion; callers get a copy.
	return append([]model.Block(nil), refs...)
}

// rebase turns root-relative paths into paths the files API resolves against
// current: relative when the file is under current, absolute otherwise. An
// absolute path keeps the short form as its label.
func rebase(refs []model.Block, root, current string) []model.Block {
	if root == current {
		return refs
	}
	out := make([]model.Block, len(refs))
	for i, b := range refs {
		rel := b.Path
		b.Path = model.DisplayPath(current, filepath.Join(root, rel))
		if filepath.IsAbs(b.Path) && b.Text == "" {
			b.Text = rel
		}
		out[i] = b
	}
	return out
}

// dedupeRefs drops a file found from both roots.
func dedupeRefs(refs []model.Block) []model.Block {
	seen := map[string]bool{}
	out := refs[:0]
	for _, b := range refs {
		k := b.Path + ":" + strconv.Itoa(b.Line)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, b)
	}
	return out
}
