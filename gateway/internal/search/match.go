// Package search finds sessions by words: in what the session list shows
// (title, folder, last message) and in what was said in them.
package search

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

type field struct {
	name string
	text string
	// shown fields are on the session's list entry, so a match in one needs
	// no excerpt.
	shown bool
}

func sessionFields(s model.Session) []field {
	return []field{
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

func messageFields(msgs []model.Message) []field {
	var out []field
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Text != "" {
				out = append(out, field{string(m.Role), b.Text, false})
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
		case r == fieldSep || r == blockSep:
		case !quoted && (r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '　'):
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return terms
}

// MatchSession reports whether every term is somewhere in the session or
// its messages, with an excerpt of the first match that the session's list
// entry does not show. Terms must be lower case; ASCII case is ignored.
func MatchSession(s model.Session, msgs []model.Message, terms []string) (*Match, bool) {
	return matchFields(append(sessionFields(s), messageFields(msgs)...), terms)
}

func matchFields(fields []field, terms []string) (*Match, bool) {
	var match *Match
	for _, term := range terms {
		found := false
		for _, f := range fields {
			if !strings.Contains(strings.ToLower(f.text), term) {
				continue
			}
			found = true
			if !f.shown && match == nil {
				match = &Match{Field: f.name, Snippet: snippet(f.text, term)}
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
