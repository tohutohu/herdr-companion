package main

import (
	"context"
	"flag"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// searchField is one piece of a session that a search term can match.
type searchField struct {
	name string
	text string
	// shown fields are already on the session's line, so a match in one
	// needs no excerpt.
	shown bool
}

type searchMatch struct {
	Field   string `json:"field"`
	Snippet string `json:"snippet"`
}

type searchHit struct {
	session model.Session
	match   *searchMatch
}

func sessionFields(s model.Session) []searchField {
	return []searchField{
		{"title", s.Title, true},
		{"title", s.OriginalTitle, false},
		{"project", s.Project, true},
		{"id", s.ID, true},
		{"cwd", s.Cwd, false},
		{"lastMessage", s.LastMessage, false},
	}
}

func messageFields(msgs []model.Message) []searchField {
	var out []searchField
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Text != "" {
				out = append(out, searchField{string(m.Role), b.Text, false})
			}
			if b.OriginalText != "" {
				out = append(out, searchField{string(m.Role), b.OriginalText, false})
			}
		}
	}
	return out
}

// matchFields reports whether every term is somewhere in fields, with an
// excerpt of the first match that the session's line does not show.
func matchFields(fields []searchField, terms []string) (*searchMatch, bool) {
	var match *searchMatch
	for _, term := range terms {
		found := false
		for _, f := range fields {
			if !strings.Contains(strings.ToLower(f.text), term) {
				continue
			}
			found = true
			if f.shown {
				break
			}
			if match == nil {
				match = &searchMatch{Field: f.name, Snippet: snippet(f.text, term)}
			}
			break
		}
		if !found {
			return nil, false
		}
	}
	return match, true
}

// snippet is the text around the first occurrence of term (lower case), on
// one line.
func snippet(text, term string) string {
	const before, width = 30, 100
	text = oneLine(text)
	lower := strings.ToLower(text)
	i := strings.Index(lower, term)
	if i < 0 || len(lower) != len(text) || !utf8.RuneStart(text[i]) {
		return clip(text, width)
	}
	start := 0
	if n := utf8.RuneCountInString(text[:i]); n > before {
		start = len(string([]rune(text[:i])[:n-before]))
	}
	out := clip(text[start:], width)
	if start > 0 {
		out = "…" + out
	}
	return out
}

func (a *app) cmdSearch(args []string) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	text := fs.Bool("text", false, "also search the conversations")
	active := fs.Bool("active", false, "only sessions that are not archived")
	archived := fs.Bool("archived", false, "only archived sessions")
	limit := fs.Int("limit", 20, "most results to print (0 = all)")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	var terms []string
	for _, p := range pos {
		for _, t := range strings.Fields(strings.ToLower(p)) {
			terms = append(terms, t)
		}
	}
	if len(terms) == 0 {
		return usagef("search takes the words to look for")
	}
	if *active && *archived {
		return usagef("search: --active and --archived exclude each other")
	}
	ctx := context.Background()
	var all []model.Session
	for _, arch := range []bool{false, true} {
		if (arch && *active) || (!arch && *archived) {
			continue
		}
		list, err := a.client.sessions(ctx, arch)
		if err != nil {
			return err
		}
		for i := range list {
			list[i].Archived = arch
		}
		all = append(all, list...)
	}
	shorts := shortIDs(all)

	var hits []searchHit
	var rest []model.Session
	for _, s := range all {
		if m, ok := matchFields(sessionFields(s), terms); ok {
			hits = append(hits, searchHit{s, m})
		} else {
			rest = append(rest, s)
		}
	}
	unread := 0
	if *text {
		var mu sync.Mutex
		var wg sync.WaitGroup
		slots := make(chan struct{}, 8)
		for _, s := range rest {
			wg.Add(1)
			slots <- struct{}{}
			go func(s model.Session) {
				defer wg.Done()
				defer func() { <-slots }()
				_, msgs, err := a.client.messages(ctx, s.ID)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					unread++
					return
				}
				if m, ok := matchFields(append(sessionFields(s), messageFields(msgs)...), terms); ok {
					hits = append(hits, searchHit{s, m})
				}
			}(s)
		}
		wg.Wait()
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if !hits[i].session.UpdatedAt.Equal(hits[j].session.UpdatedAt) {
			return hits[i].session.UpdatedAt.After(hits[j].session.UpdatedAt)
		}
		return hits[i].session.ID < hits[j].session.ID
	})
	more := 0
	if *limit > 0 && len(hits) > *limit {
		more = len(hits) - *limit
		hits = hits[:*limit]
	}

	if a.json {
		type hitJSON struct {
			sessionJSON
			Match *searchMatch `json:"match,omitempty"`
		}
		out := []hitJSON{}
		for _, h := range hits {
			out = append(out, hitJSON{sessionJSON{h.session, shorts[h.session.ID], a.isSelf(h.session)}, h.match})
		}
		return a.printJSON(map[string]any{"sessions": out, "more": more, "unread": unread})
	}
	for _, h := range hits {
		line := a.sessionLine(h.session, shorts[h.session.ID])
		if h.session.Archived {
			line += "  (archived)"
		}
		fmt.Fprintln(a.out, line)
		if h.match != nil {
			fmt.Fprintf(a.out, "      %s: %s\n", h.match.Field, h.match.Snippet)
		}
	}
	if len(hits) == 0 {
		fmt.Fprintf(a.out, "no session matches %q\n", strings.Join(terms, " "))
		if !*text {
			fmt.Fprintln(a.out, "(titles, folders and last messages only; add --text to search the conversations)")
		}
	}
	if more > 0 {
		fmt.Fprintf(a.out, "(+%d more: --limit 0)\n", more)
	}
	if unread > 0 {
		fmt.Fprintf(a.out, "(%d session%s could not be read)\n", unread, plural(unread))
	}
	return nil
}
