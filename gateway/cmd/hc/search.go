package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/search"
)

type searchHit struct {
	session model.Session
	match   *search.Match
}

// searchResult is what the Gateway's search answers, or what hc found
// itself on a Gateway without one.
type searchResult struct {
	hits []searchHit
	more int
	// indexing: the Gateway is still reading sessions, so hits may be missing.
	indexing bool
	// unread counts the sessions whose conversation could not be read.
	unread int
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
	// Each argument is a word, or a phrase when it was quoted in the shell.
	var queries []string
	for _, p := range pos {
		if p = strings.TrimSpace(strings.ReplaceAll(p, `"`, " ")); p == "" {
			continue
		}
		if strings.ContainsAny(p, " \t\n　") {
			p = `"` + p + `"`
		}
		queries = append(queries, p)
	}
	if len(queries) == 0 {
		return usagef("search takes the words to look for")
	}
	if *active && *archived {
		return usagef("search: --active and --archived exclude each other")
	}
	if *limit < 0 {
		return usagef("search: --limit must not be negative")
	}
	ctx := context.Background()
	res, err := a.client.search(ctx, queries, *text, *active, *archived, *limit)
	var apiErr *apiError
	if errors.As(err, &apiErr) && apiErr.status == http.StatusNotFound {
		// A Gateway from before it could search.
		res, err = a.searchLocally(ctx, queries, *text, *active, *archived, *limit)
	}
	if err != nil {
		return err
	}
	// Reading both session lists for the ids hc ls shows would cost more
	// than the search; 8 characters of an id rarely name two sessions.
	var found []model.Session
	for _, h := range res.hits {
		found = append(found, h.session)
	}
	shorts := shortIDs(found)
	short := func(s model.Session) string { return shorts[s.ID] }
	hits, more, unread := res.hits, res.more, res.unread

	if a.json {
		type hitJSON struct {
			sessionJSON
			Match *search.Match `json:"match,omitempty"`
		}
		out := []hitJSON{}
		for _, h := range hits {
			out = append(out, hitJSON{sessionJSON{h.session, short(h.session), a.isSelf(h.session)}, h.match})
		}
		return a.printJSON(map[string]any{"sessions": out, "more": more, "unread": unread, "indexing": res.indexing})
	}
	for _, h := range hits {
		line := a.sessionLine(h.session, short(h.session))
		if h.session.Archived {
			line += "  (archived)"
		}
		fmt.Fprintln(a.out, line)
		if h.match != nil {
			fmt.Fprintf(a.out, "      %s: %s\n", h.match.Field, h.match.Snippet)
		}
	}
	if len(hits) == 0 {
		fmt.Fprintf(a.out, "no session matches %s\n", strings.Join(queries, " "))
		if !*text {
			fmt.Fprintln(a.out, "(titles, folders and last messages only; add --text to search the conversations)")
		}
	}
	if more > 0 {
		fmt.Fprintf(a.out, "(+%d more: --limit 0)\n", more)
	}
	if res.indexing {
		fmt.Fprintln(a.out, "(the Gateway is still reading sessions; search again in a moment for the rest)")
	}
	if unread > 0 {
		fmt.Fprintf(a.out, "(%d session%s could not be read)\n", unread, plural(unread))
	}
	return nil
}

// searchLocally searches by reading the session lists, and with text every
// conversation that did not match, for a Gateway that cannot search.
func (a *app) searchLocally(ctx context.Context, queries []string, text, active, archived bool, limit int) (searchResult, error) {
	var res searchResult
	var terms []string
	for _, q := range queries {
		terms = append(terms, search.ParseQuery(q)...)
	}
	var all []model.Session
	for _, arch := range []bool{false, true} {
		if (arch && active) || (!arch && archived) {
			continue
		}
		list, err := a.client.sessions(ctx, arch)
		if err != nil {
			return res, err
		}
		for i := range list {
			list[i].Archived = arch
		}
		all = append(all, list...)
	}
	var rest []model.Session
	for _, s := range all {
		if m, ok := search.MatchSession(s, nil, terms); ok {
			res.hits = append(res.hits, searchHit{s, m})
		} else {
			rest = append(rest, s)
		}
	}
	if text {
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
					res.unread++
					return
				}
				if m, ok := search.MatchSession(s, msgs, terms); ok {
					res.hits = append(res.hits, searchHit{s, m})
				}
			}(s)
		}
		wg.Wait()
	}
	sort.SliceStable(res.hits, func(i, j int) bool {
		if !res.hits[i].session.UpdatedAt.Equal(res.hits[j].session.UpdatedAt) {
			return res.hits[i].session.UpdatedAt.After(res.hits[j].session.UpdatedAt)
		}
		return res.hits[i].session.ID < res.hits[j].session.ID
	})
	if limit > 0 && len(res.hits) > limit {
		res.more = len(res.hits) - limit
		res.hits = res.hits[:limit]
	}
	return res, nil
}
