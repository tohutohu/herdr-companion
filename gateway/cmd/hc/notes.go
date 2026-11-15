package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// cmdNote manages the notes left on sessions: instructions for the agents
// that watch a session ("answer the review on its PR", "archive it once
// merged"). The Gateway keeps them; the session's own agent never gets them.
func (a *app) cmdNote(args []string) error {
	if len(args) == 0 {
		return a.noteList(nil)
	}
	sub, rest := args[0], args[1:]
	cmds := map[string]func([]string) error{
		"ls": a.noteList, "list": a.noteList, "show": a.noteShow, "add": a.noteAdd,
		"edit": a.noteEdit, "update": a.noteEdit, "rm": a.noteRemove, "delete": a.noteRemove, "clear": a.noteClear,
	}
	run, ok := cmds[sub]
	if !ok {
		return usagef("unknown note subcommand %q (ls, show, add, edit, rm, clear)", sub)
	}
	return run(rest)
}

// errNoNotes is what an older Gateway, without /v1/notes, gives.
var errNoNotes = errors.New("this Gateway does not keep notes (update the Gateway)")

func (a *app) checkNotes(ctx context.Context) error {
	var caps struct {
		Notes bool `json:"notes"`
	}
	err := a.client.do(ctx, http.MethodGet, "/v1/capabilities", nil, &caps)
	var ae *apiError
	if (errors.As(err, &ae) && ae.status == http.StatusNotFound) || (err == nil && !caps.Notes) {
		return errNoNotes
	}
	return err
}

// noteSession resolves the session a note command names; "self" is the
// session running this command.
func (a *app) noteSession(ctx context.Context, ref string) (model.Session, error) {
	if ref == "self" {
		if a.selfID == "" {
			return model.Session{}, errors.New(`"self" needs HC_SESSION_ID (or CLAUDE_CODE_SESSION_ID / CODEX_THREAD_ID): this process does not run in a session`)
		}
		ref = a.selfID
	}
	return a.resolve(ctx, ref)
}

func (a *app) allNotes(ctx context.Context, archived bool) ([]model.Note, error) {
	path := "/v1/notes"
	if archived {
		path += "?archived=true"
	}
	var res struct {
		Notes []model.Note `json:"notes"`
	}
	err := a.client.do(ctx, http.MethodGet, path, nil, &res)
	var ae *apiError
	if errors.As(err, &ae) && ae.status == http.StatusNotFound {
		return nil, errNoNotes
	}
	return res.Notes, err
}

// resolveNote finds a note by id or a unique id prefix, archived sessions'
// notes included.
func (a *app) resolveNote(ctx context.Context, ref string) (model.Note, error) {
	if ref == "" {
		return model.Note{}, usagef("note ID required (see \"hc note ls\")")
	}
	list, err := a.allNotes(ctx, true)
	if err != nil {
		return model.Note{}, err
	}
	var found []model.Note
	for _, n := range list {
		if n.ID == ref {
			return n, nil
		}
		if strings.HasPrefix(n.ID, ref) {
			found = append(found, n)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return model.Note{}, fmt.Errorf("no note matches %q (see \"hc note ls --archived\")", ref)
	}
	var ids []string
	for _, n := range found {
		ids = append(ids, n.ID)
	}
	return model.Note{}, fmt.Errorf("%q matches %d notes: %s", ref, len(found), strings.Join(ids, ", "))
}

// parseTTL reads a lifetime such as 90m, 12h or 3d.
func parseTTL(v string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(v, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, usagef("--ttl %q: use a positive duration such as 90m, 12h or 3d", v)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, usagef("--ttl %q: use a positive duration such as 90m, 12h or 3d", v)
	}
	return d, nil
}

func (a *app) noteList(args []string) error {
	fs := flag.NewFlagSet("note ls", flag.ContinueOnError)
	archived := fs.Bool("archived", false, "include the notes of archived sessions")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return usagef("note ls takes at most one session ID")
	}
	ctx := context.Background()
	if err := a.checkNotes(ctx); err != nil {
		return err
	}
	var list []model.Note
	var only model.Session
	if len(pos) == 1 {
		if only, err = a.noteSession(ctx, pos[0]); err != nil {
			return err
		}
		list = only.Notes
	} else if list, err = a.allNotes(ctx, *archived); err != nil {
		return err
	}
	if a.json {
		if list == nil {
			list = []model.Note{}
		}
		return a.printJSON(map[string]any{"notes": list})
	}
	if len(list) == 0 {
		if only.ID != "" {
			fmt.Fprintf(a.out, "no notes on %s (add one: hc note add %s TEXT)\n", only.ID, nativeID(only.ID))
		} else {
			fmt.Fprintln(a.out, "no notes (add one: hc note add ID TEXT)")
		}
		return nil
	}
	// Head each session's notes with its list line, so the reader sees what
	// the session is doing next to what to do about it.
	active, err := a.client.sessions(ctx, false)
	if err != nil {
		return err
	}
	shorts := shortIDs(active)
	byID := map[string]model.Session{}
	for _, s := range active {
		byID[s.ID] = s
	}
	if only.ID != "" {
		byID[only.ID] = only
		if shorts[only.ID] == "" {
			shorts[only.ID] = clipID(nativeID(only.ID), 8)
		}
	}
	prev := ""
	for _, n := range list {
		if n.SessionID != prev {
			prev = n.SessionID
			if s, ok := byID[n.SessionID]; ok {
				line := a.sessionLine(s, shorts[s.ID])
				if s.Archived {
					line += "  (archived)"
				}
				fmt.Fprintln(a.out, line)
			} else {
				fmt.Fprintf(a.out, "  %s  (archived or not listed)\n", n.SessionID)
			}
		}
		writeNote(a.out, n, "    ")
	}
	return nil
}

// writeNote prints a note in full, wherever hc shows one (ls, search,
// show, changes, wait, until, note): the reader never needs another command
// to know what the note says, who left it and how long it stays. A one-line
// note stays on one line.
func writeNote(w io.Writer, n model.Note, prefix string) {
	when := age(n.UpdatedAt)
	if when != "now" {
		when += " ago"
	}
	facts := []string{when}
	if n.Author != "" {
		facts = append(facts, "by "+shortSession(n.Author))
	}
	if n.UpdatedBy != "" && n.UpdatedBy != n.Author {
		facts = append(facts, "edited by "+shortSession(n.UpdatedBy))
	}
	if n.ExpiresAt != nil {
		facts = append(facts, "expires in "+until(*n.ExpiresAt))
	}
	head := fmt.Sprintf("%snote %s (%s):", prefix, n.ID, strings.Join(facts, ", "))
	text := strings.TrimSpace(n.Text)
	if !strings.Contains(text, "\n") {
		fmt.Fprintln(w, head+" "+text)
		return
	}
	fmt.Fprintln(w, head)
	fmt.Fprintln(w, indent(text, prefix+"  "))
}

// writeNotes prints every note of a session under it.
func writeNotes(w io.Writer, s model.Session, prefix string) {
	for _, n := range s.Notes {
		writeNote(w, n, prefix)
	}
}

// shortSession is a session id with its native part cut to 8 characters,
// as hc prints ids elsewhere: "claude:2964188e".
func shortSession(id string) string {
	provider, native, ok := strings.Cut(id, ":")
	if !ok {
		return clipID(id, 8)
	}
	return provider + ":" + clipID(native, 8)
}

// until is how long until t, in the units age uses.
func until(t time.Time) string {
	d := time.Until(t)
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func (a *app) noteShow(args []string) error {
	pos, err := parseFlags(flag.NewFlagSet("note show", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("note show takes one note ID")
	}
	ctx := context.Background()
	if err := a.checkNotes(ctx); err != nil {
		return err
	}
	n, err := a.resolveNote(ctx, pos[0])
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(n)
	}
	fmt.Fprintf(a.out, "session %s\n", n.SessionID)
	writeNote(a.out, n, "")
	return nil
}

func (a *app) noteAdd(args []string) error {
	fs := flag.NewFlagSet("note add", flag.ContinueOnError)
	ttl := fs.String("ttl", "", "forget the note after this long (90m, 12h, 3d)")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		return usagef("note add takes a session ID and the text (\"-\" reads it from stdin)")
	}
	text, err := a.textArg(pos[1:])
	if err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return usagef("note text is empty")
	}
	body := map[string]any{"text": text}
	if a.selfID != "" {
		body["author"] = a.selfID
	}
	if *ttl != "" {
		d, err := parseTTL(*ttl)
		if err != nil {
			return err
		}
		body["expiresAt"] = time.Now().Add(d).UTC().Format(time.RFC3339)
	}
	ctx := context.Background()
	if err := a.checkNotes(ctx); err != nil {
		return err
	}
	s, err := a.noteSession(ctx, pos[0])
	if err != nil {
		return err
	}
	var n model.Note
	if err := a.client.do(ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(s.ID)+"/notes", body, &n); err != nil {
		return err
	}
	if a.json {
		return a.printJSON(n)
	}
	fmt.Fprintf(a.out, "added note %s to %s\n", n.ID, s.ID)
	return nil
}

func (a *app) noteEdit(args []string) error {
	fs := flag.NewFlagSet("note edit", flag.ContinueOnError)
	ttl := fs.String("ttl", "", "forget the note after this long from now (90m, 12h, 3d)")
	noExpiry := fs.Bool("no-expiry", false, "keep the note until it is removed")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 {
		return usagef("note edit takes a note ID and the new text, --ttl or --no-expiry")
	}
	if *ttl != "" && *noExpiry {
		return usagef("note edit: --ttl and --no-expiry exclude each other")
	}
	body := map[string]any{}
	if len(pos) > 1 {
		text, err := a.textArg(pos[1:])
		if err != nil {
			return err
		}
		if strings.TrimSpace(text) == "" {
			return usagef("note text is empty")
		}
		body["text"] = text
	}
	switch {
	case *noExpiry:
		body["expiresAt"] = nil
	case *ttl != "":
		d, err := parseTTL(*ttl)
		if err != nil {
			return err
		}
		body["expiresAt"] = time.Now().Add(d).UTC().Format(time.RFC3339)
	}
	if len(body) == 0 {
		return usagef("note edit: give the new text, --ttl or --no-expiry")
	}
	if a.selfID != "" {
		body["author"] = a.selfID
	}
	ctx := context.Background()
	if err := a.checkNotes(ctx); err != nil {
		return err
	}
	n, err := a.resolveNote(ctx, pos[0])
	if err != nil {
		return err
	}
	if err := a.client.do(ctx, http.MethodPatch, "/v1/notes/"+url.PathEscape(n.ID), body, &n); err != nil {
		return err
	}
	if a.json {
		return a.printJSON(n)
	}
	fmt.Fprintf(a.out, "updated note %s on %s\n", n.ID, n.SessionID)
	return nil
}

func (a *app) noteRemove(args []string) error {
	pos, err := parseFlags(flag.NewFlagSet("note rm", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return usagef("note rm takes one or more note IDs (hc note clear ID removes all of a session's)")
	}
	ctx := context.Background()
	if err := a.checkNotes(ctx); err != nil {
		return err
	}
	// Resolve them all first, so a typo removes nothing.
	var targets []model.Note
	for _, ref := range pos {
		n, err := a.resolveNote(ctx, ref)
		if err != nil {
			return err
		}
		targets = append(targets, n)
	}
	var removed []model.Note
	for _, n := range targets {
		if err := a.client.do(ctx, http.MethodDelete, "/v1/notes/"+url.PathEscape(n.ID), nil, nil); err != nil {
			return err
		}
		removed = append(removed, n)
		if !a.json {
			fmt.Fprintf(a.out, "removed note %s from %s\n", n.ID, n.SessionID)
		}
	}
	if a.json {
		return a.printJSON(map[string]any{"notes": removed})
	}
	return nil
}

func (a *app) noteClear(args []string) error {
	pos, err := parseFlags(flag.NewFlagSet("note clear", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("note clear takes one session ID")
	}
	ctx := context.Background()
	if err := a.checkNotes(ctx); err != nil {
		return err
	}
	s, err := a.noteSession(ctx, pos[0])
	if err != nil {
		return err
	}
	var res struct {
		Notes []model.Note `json:"notes"`
	}
	if err := a.client.do(ctx, http.MethodDelete, "/v1/sessions/"+url.PathEscape(s.ID)+"/notes", nil, &res); err != nil {
		return err
	}
	if a.json {
		if res.Notes == nil {
			res.Notes = []model.Note{}
		}
		return a.printJSON(res)
	}
	fmt.Fprintf(a.out, "removed %d note(s) from %s\n", len(res.Notes), s.ID)
	return nil
}
