// Package sessionmatch decides whether a session holds the words of a
// search: in what the session list shows (title, folder, last message) and
// in what was said in it. It depends on the model only, so the hc client can
// use it as well as the gateway's index (internal/search).
package sessionmatch

import (
	"strings"
	"unicode/utf8"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// Match says where the words were found when that is not something a
// session's list entry shows already.
type Match struct {
	// Field is "cwd", "lastMessage", "title" (the untranslated one), "model"
	// or, for a message, its role.
	Field   string `json:"field"`
	Snippet string `json:"snippet"`
}

// Field is one searchable piece of a session.
type Field struct {
	Name string
	Text string
	// Shown fields are on the session's list entry, so a match in one needs
	// no excerpt.
	Shown bool
}

const (
	// FieldSep and BlockSep never occur in a term, so text joined with them
	// cannot match across the join.
	FieldSep = '\x1f'
	BlockSep = '\x1e'
)

// SessionFields are the fields of a session's list entry.
func SessionFields(s model.Session) []Field {
	return []Field{
		{"title", s.Title, true},
		{"title", s.OriginalTitle, false},
		{"project", s.Project, true},
		{"id", s.ID, true},
		{"provider", s.Provider, true},
		{"provider", s.ProviderName, true},
		{"cwd", s.Cwd, false},
		{"lastMessage", s.LastMessage, false},
		{"model", s.Model, false},
	}
}

// MessageFields are the texts of a conversation, named by who said them.
func MessageFields(msgs []model.Message) []Field {
	var out []Field
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Text != "" {
				out = append(out, Field{string(m.Role), b.Text, false})
			}
		}
	}
	return out
}

// ParseQuery splits what a person typed into the terms that must all be
// found: words, or phrases in double quotes. Terms are lower case.
func ParseQuery(q string) []string {
	var terms []string
	var cur strings.Builder
	quoted := false
	flush := func() {
		if t := strings.TrimSpace(cur.String()); t != "" {
			terms = append(terms, t)
		}
		cur.Reset()
	}
	for _, r := range strings.ToLower(q) {
		switch {
		case r == '"':
			flush()
			quoted = !quoted
		case r == FieldSep || r == BlockSep:
		case !quoted && (r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '　'):
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return terms
}

// Session reports whether every term is somewhere in the session or its
// messages; see Fields.
func Session(s model.Session, msgs []model.Message, terms []string) (*Match, bool) {
	return Fields(append(SessionFields(s), MessageFields(msgs)...), terms)
}

// Fields reports whether every term is in one of fields, with an excerpt of
// the first match that the session's list entry does not show. Terms must be
// lower case, as ParseQuery returns them.
func Fields(fields []Field, terms []string) (*Match, bool) {
	var match *Match
	for _, term := range terms {
		found := false
		for _, f := range fields {
			if !strings.Contains(strings.ToLower(f.Text), term) {
				continue
			}
			found = true
			if !f.Shown && match == nil {
				match = &Match{Field: f.Name, Snippet: snippet(f.Text, term)}
			}
			break
		}
		if !found {
			return nil, false
		}
	}
	return match, true
}

// snippet is the text around the first occurrence of term, on one line.
func snippet(text, term string) string {
	const before, width = 30, 100
	text = strings.Join(strings.Fields(text), " ")
	lower := strings.ToLower(text)
	i := strings.Index(lower, term)
	if i < 0 || len(lower) != len(text) || !utf8.RuneStart(text[i]) {
		i = 0
	}
	runes := []rune(text[i:])
	head := []rune(text[:i])
	out := ""
	if len(head) > before {
		head = head[len(head)-before:]
		out = "…"
	}
	out += string(head)
	if rest := width - len(head); len(runes) > rest {
		return out + string(runes[:rest]) + "…"
	}
	return out + string(runes)
}
