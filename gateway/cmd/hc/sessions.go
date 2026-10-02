package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// shortIDs gives each session the shortest prefix of its native id (at
// least 8 characters) that no other listed session shares.
func shortIDs(sessions []model.Session) map[string]string {
	out := map[string]string{}
	for _, s := range sessions {
		native := nativeID(s.ID)
		n := 8
		for ; n < len(native); n++ {
			unique := true
			for _, o := range sessions {
				if o.ID != s.ID && strings.HasPrefix(nativeID(o.ID), native[:n]) {
					unique = false
					break
				}
			}
			if unique {
				break
			}
		}
		out[s.ID] = clipID(native, n)
	}
	return out
}

func nativeID(id string) string {
	if _, native, ok := strings.Cut(id, ":"); ok {
		return native
	}
	return id
}

// matchSession finds the one session ref names: a full id, a native id, a
// pane id, or a unique prefix of either id.
func matchSession(sessions []model.Session, ref string) (model.Session, []model.Session) {
	for _, s := range sessions {
		if s.ID == ref || nativeID(s.ID) == ref || (s.PaneID != "" && s.PaneID == ref && s.Status != model.StatusOffline) {
			return s, nil
		}
	}
	var found []model.Session
	for _, s := range sessions {
		if strings.HasPrefix(nativeID(s.ID), ref) || strings.HasPrefix(s.ID, ref) {
			found = append(found, s)
		}
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return model.Session{}, found
}

// resolve turns what the reader typed into a session, looking at archived
// sessions only when no active one matches.
func (a *app) resolve(ctx context.Context, ref string) (model.Session, error) {
	if ref == "" {
		return model.Session{}, usagef("session ID required (see \"hc ls\")")
	}
	for _, archived := range []bool{false, true} {
		list, err := a.client.sessions(ctx, archived)
		if err != nil {
			return model.Session{}, err
		}
		s, many := matchSession(list, ref)
		if s.ID != "" {
			return s, nil
		}
		if len(many) > 1 {
			var ids []string
			for _, m := range many {
				ids = append(ids, m.ID)
			}
			return model.Session{}, fmt.Errorf("%q matches %d sessions: %s", ref, len(many), strings.Join(ids, ", "))
		}
	}
	return model.Session{}, fmt.Errorf("no session matches %q (see \"hc ls\", or \"hc ls --archived\")", ref)
}

func (a *app) isSelf(s model.Session) bool {
	return a.selfPane != "" && s.PaneID == a.selfPane && s.Status != model.StatusOffline
}

func needsYou(s model.Status) bool {
	return s == model.StatusWaitingInput || s == model.StatusWaitingApproval
}

func (a *app) printJSON(v any) error {
	enc := json.NewEncoder(a.out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	return enc.Encode(v)
}

// sessionLine is the one-line form used by ls, changes and wait.
func (a *app) sessionLine(s model.Session, short string) string {
	mark := " "
	if needsYou(s.Status) {
		mark = "!"
	}
	line := fmt.Sprintf("%s %-8s %-16s %4s  %-6s %s", mark, short, s.Status, age(s.UpdatedAt), s.Provider, s.Project)
	if s.Title != "" {
		line += " · " + clip(oneLine(s.Title), 60)
	}
	if a.isSelf(s) {
		line += "  (you)"
	}
	return line
}

func age(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

type sessionJSON struct {
	model.Session
	Short string `json:"short"`
	Self  bool   `json:"self,omitempty"`
}

func (a *app) cmdList(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	archived := fs.Bool("archived", false, "list archived sessions")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	list, err := a.client.sessions(context.Background(), *archived)
	if err != nil {
		return err
	}
	shorts := shortIDs(list)
	if a.json {
		out := []sessionJSON{}
		for _, s := range list {
			out = append(out, sessionJSON{s, shorts[s.ID], a.isSelf(s)})
		}
		return a.printJSON(map[string]any{"sessions": out})
	}
	if len(list) == 0 {
		fmt.Fprintln(a.out, "no sessions")
		return nil
	}
	for _, s := range list {
		fmt.Fprintln(a.out, a.sessionLine(s, shorts[s.ID]))
	}
	return nil
}

func (a *app) cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	full := fs.Bool("full", false, "do not shorten long text")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("show takes one session ID")
	}
	ctx := context.Background()
	s, err := a.resolve(ctx, pos[0])
	if err != nil {
		return err
	}
	s, msgs, err := a.client.messages(ctx, s.ID)
	if err != nil {
		return err
	}
	pending := pendingInteractions(msgs)
	var last *model.Message
	rows := foldRows(msgs)
	for i := len(rows) - 1; i >= 0; i-- {
		if !rows[i].isTools() && rows[i].msg.Role == model.RoleAssistant {
			last = &rows[i].msg
			break
		}
	}
	if a.json {
		return a.printJSON(map[string]any{"session": s, "pending": pending, "lastReport": last})
	}
	short := pos[0]
	max := 1500
	if *full {
		max = 0
	}
	w := a.out
	fmt.Fprintf(w, "%s  %s", s.ID, s.Status)
	if a.isSelf(s) {
		fmt.Fprint(w, "  (you)")
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "project: %s · cwd: %s\n", s.Project, s.Cwd)
	if s.Title != "" {
		fmt.Fprintf(w, "title: %s\n", s.Title)
	}
	var facts []string
	facts = append(facts, "updated "+age(s.UpdatedAt)+" ago")
	if s.PaneID != "" {
		facts = append(facts, "pane "+s.PaneID)
	}
	if s.Model != "" {
		facts = append(facts, strings.TrimSpace(s.Model+" "+s.Effort))
	}
	if s.Mode != "" {
		facts = append(facts, "mode "+s.Mode)
	}
	if s.Context != nil {
		facts = append(facts, fmt.Sprintf("context %d%%", s.Context.UsedPercent))
	}
	if s.Cost != nil {
		facts = append(facts, fmt.Sprintf("$%.2f", s.Cost.USD))
	}
	if !s.CanSend {
		facts = append(facts, "not running (hc resume to restart)")
	}
	fmt.Fprintln(w, strings.Join(facts, " · "))
	o := renderOpts{short: short, maxChars: max}
	for _, ia := range pending {
		renderInteraction(w, ia, short)
	}
	if last != nil {
		fmt.Fprintln(w, "last report:")
		renderRow(w, row{msg: *last}, o)
	}
	if len(rows) > 0 && rows[len(rows)-1].isTools() && s.Status == model.StatusRunning {
		fmt.Fprintln(w, "now:")
		renderRow(w, rows[len(rows)-1], o)
	}
	return nil
}

func (a *app) cmdRead(args []string) error {
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	all := fs.Bool("all", false, "the whole transcript")
	last := fs.Int("last", 0, "the last N rows, whatever was read before")
	tools := fs.Bool("tools", false, "list every tool call and output")
	full := fs.Bool("full", false, "do not shorten long text")
	peek := fs.Bool("peek", false, "do not move the read cursor")
	msgID := fs.String("msg", "", "print one message in full")
	maxChars := fs.Int("max", 2000, "characters per text block before it is cut")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("read takes one session ID")
	}
	ctx := context.Background()
	s, err := a.resolve(ctx, pos[0])
	if err != nil {
		return err
	}
	s, msgs, err := a.client.messages(ctx, s.ID)
	if err != nil {
		return err
	}
	o := renderOpts{short: pos[0], maxChars: *maxChars, tools: *tools}
	if *full {
		o.maxChars = 0
	}
	if *msgID != "" {
		m, err := findMessage(msgs, *msgID)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(m)
		}
		o.maxChars, o.tools = 0, true
		for _, r := range foldRows([]model.Message{m}) {
			if r.isTools() {
				for _, p := range r.tools {
					fmt.Fprintln(a.out, strings.TrimRight(p.text, "\n"))
				}
				continue
			}
			renderRow(a.out, r, o)
		}
		return nil
	}

	mark, err := a.state.loadRead(a.cursor, s.ID)
	if err != nil {
		return err
	}
	start := newSince(msgs, mark)
	firstRead := mark == nil
	shown := msgs[start:]
	rows := foldRows(shown)
	skipped := 0
	limit := *last
	if firstRead && limit == 0 && !*all {
		limit = 8
	}
	if *all {
		rows, start, limit = foldRows(msgs), 0, 0
	} else if *last > 0 {
		rows = foldRows(msgs)
	}
	if limit > 0 && len(rows) > limit {
		skipped = len(rows) - limit
		rows = rows[skipped:]
	}
	// Questions above what is shown still need an answer.
	earlier := pendingInteractions(msgs[:start])

	if !*peek {
		if m, ok := markAt(msgs); ok {
			if err := a.state.saveRead(a.cursor, s.ID, m); err != nil {
				return err
			}
		}
	}
	if a.json {
		var outMsgs []model.Message = shown
		if *all || *last > 0 {
			outMsgs = msgs
		}
		return a.printJSON(map[string]any{
			"session": s, "messages": outMsgs, "pending": pendingInteractions(msgs), "firstRead": firstRead,
		})
	}
	w := a.out
	fmt.Fprintf(w, "%s %s · %s", pos[0], s.Status, s.Project)
	if s.Title != "" {
		fmt.Fprintf(w, " · %s", clip(oneLine(s.Title), 60))
	}
	fmt.Fprintln(w)
	if skipped > 0 {
		fmt.Fprintf(w, "(%d earlier rows not shown; --all for everything, --last N for more)\n", skipped)
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, "(no new messages)")
	}
	for _, r := range rows {
		renderRow(w, r, o)
	}
	for _, ia := range earlier {
		fmt.Fprintln(w, "still pending from earlier:")
		renderInteraction(w, ia, pos[0])
	}
	return nil
}

func (a *app) cmdExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	outPath := fs.String("out", "", "file to write (default: in the hc state directory)")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("export takes one session ID")
	}
	ctx := context.Background()
	s, err := a.resolve(ctx, pos[0])
	if err != nil {
		return err
	}
	s, msgs, err := a.client.messages(ctx, s.ID)
	if err != nil {
		return err
	}
	path := *outPath
	if path == "" {
		path = filepath.Join(a.state.dir, "exports", safeName(s.ID)+".md")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	writeMarkdown(f, s, msgs)
	if err := f.Close(); err != nil {
		return err
	}
	st, _ := os.Stat(path)
	if a.json {
		return a.printJSON(map[string]any{"path": path, "messages": len(msgs), "bytes": st.Size()})
	}
	fmt.Fprintf(a.out, "%s\n%d messages, %d bytes. Read or grep this file; it is rewritten on the next export.\n", path, len(msgs), st.Size())
	return nil
}

// event is one difference between the reader's snapshot and now.
type event struct {
	Kind      string        `json:"kind"` // new, status, updated, gone
	From      model.Status  `json:"from,omitempty"`
	Session   model.Session `json:"session"`
	Short     string        `json:"short"`
	Attention bool          `json:"attention"`
	// Pending is filled by wait for sessions waiting on an answer.
	Pending []model.Interaction `json:"pending,omitempty"`
}

// attention says whether moving from one status to another is something
// the reader must act on: a question, an approval, or a turn that ended.
func attention(from, to model.Status) bool {
	switch to {
	case model.StatusWaitingInput, model.StatusWaitingApproval, model.StatusCompleted, model.StatusFailed:
		return true
	case model.StatusIdle, model.StatusOffline:
		return from == model.StatusRunning
	}
	return false
}

// diffSessions compares the snapshot with the current list. With no
// snapshot yet only the sessions waiting for an answer are reported.
func (a *app) diffSessions(prev *snapshot, cur []model.Session) []event {
	shorts := shortIDs(cur)
	var out []event
	seenNow := map[string]bool{}
	for _, s := range cur {
		seenNow[s.ID] = true
		if a.isSelf(s) {
			continue
		}
		ev := event{Session: s, Short: shorts[s.ID]}
		if prev == nil {
			if !needsYou(s.Status) {
				continue
			}
			ev.Kind, ev.Attention = "status", true
			out = append(out, ev)
			continue
		}
		old, ok := prev.Sessions[s.ID]
		switch {
		case !ok:
			ev.Kind, ev.Attention = "new", attention(model.StatusRunning, s.Status) && s.Status != model.StatusOffline
		case old.Status != s.Status:
			ev.Kind, ev.From, ev.Attention = "status", old.Status, attention(old.Status, s.Status)
		case s.UpdatedAt.After(old.UpdatedAt):
			// Same status but newer: a whole turn may have run between two
			// looks (idle → running → idle).
			ev.Kind, ev.From = "updated", old.Status
			ev.Attention = s.Status != model.StatusRunning && s.Status != model.StatusOffline
		default:
			continue
		}
		out = append(out, ev)
	}
	if prev != nil {
		var gone []string
		for id := range prev.Sessions {
			if !seenNow[id] {
				gone = append(gone, id)
			}
		}
		sort.Strings(gone)
		for _, id := range gone {
			old := prev.Sessions[id]
			provider, _, _ := strings.Cut(id, ":")
			out = append(out, event{Kind: "gone", From: old.Status, Short: clipID(nativeID(id), 8),
				Session: model.Session{ID: id, Provider: provider, Status: old.Status, UpdatedAt: old.UpdatedAt}})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Attention && !out[j].Attention })
	return out
}

func (a *app) printEvents(w io.Writer, evs []event) {
	for _, ev := range evs {
		s := ev.Session
		change := ""
		switch ev.Kind {
		case "new":
			change = "new"
		case "status":
			if ev.From == "" {
				change = string(s.Status)
			} else {
				change = fmt.Sprintf("%s → %s", ev.From, s.Status)
			}
		case "updated":
			change = string(s.Status) + " (new activity)"
		case "gone":
			change = "gone (archived or too old)"
		}
		mark := " "
		if ev.Attention {
			mark = "!"
		}
		line := fmt.Sprintf("%s %-8s %s", mark, ev.Short, change)
		if s.Project != "" {
			line += "  " + s.Project
		}
		if s.Title != "" {
			line += " · " + clip(oneLine(s.Title), 50)
		}
		fmt.Fprintln(w, line)
		if ev.Attention && s.LastMessage != "" && ev.Kind != "gone" {
			fmt.Fprintf(w, "    last: %s\n", clip(oneLine(s.LastMessage), 300))
		}
		for _, ia := range ev.Pending {
			out := &strings.Builder{}
			renderInteraction(out, ia, ev.Short)
			fmt.Fprintln(w, indent(out.String(), "    "))
		}
	}
	for _, ev := range evs {
		if ev.Attention {
			fmt.Fprintf(w, "next: hc read %s   (or hc show ID)\n", ev.Short)
			break
		}
	}
}

func (a *app) cmdChanges(args []string) error {
	fs := flag.NewFlagSet("changes", flag.ContinueOnError)
	peek := fs.Bool("peek", false, "do not move the cursor")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	prev, err := a.state.loadSnapshot(a.cursor)
	if err != nil {
		return err
	}
	cur, err := a.client.sessions(context.Background(), false)
	if err != nil {
		return err
	}
	evs := a.diffSessions(prev, cur)
	if !*peek {
		if err := a.state.saveSnapshot(a.cursor, cur, time.Now()); err != nil {
			return err
		}
	}
	if a.json {
		return a.printJSON(map[string]any{"events": nonNil(evs), "firstRun": prev == nil})
	}
	if prev == nil {
		fmt.Fprintf(a.out, "first run for cursor %q: baseline saved (%d sessions). Later calls show only changes.\n", a.cursor, len(cur))
	} else if len(evs) == 0 {
		fmt.Fprintf(a.out, "no changes since %s\n", clock(prev.TakenAt))
	}
	a.printEvents(a.out, evs)
	return nil
}

func nonNil(evs []event) []event {
	if evs == nil {
		return []event{}
	}
	return evs
}

// pollInterval is how often wait asks the Gateway; tests shorten it.
var pollInterval = 2 * time.Second

func (a *app) cmdWait(args []string) error {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	timeout := fs.Duration("timeout", 110*time.Second, "give up after this long (e.g. 9m, 1h)")
	anyChange := fs.Bool("any", false, "return on any change, not only those that need you")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	only := map[string]bool{}
	for _, ref := range pos {
		s, err := a.resolve(ctx, ref)
		if err != nil {
			return err
		}
		only[s.ID] = true
	}
	prev, err := a.state.loadSnapshot(a.cursor)
	if err != nil {
		return err
	}
	if prev == nil {
		// No starting point yet: take one now, so the end of the current
		// turns counts, and report what is already waiting for an answer.
		cur, err := a.client.sessions(ctx, false)
		if err != nil {
			return err
		}
		if err := a.state.saveSnapshot(a.cursor, cur, time.Now()); err != nil {
			return err
		}
		if evs := a.diffSessions(nil, cur); len(only) == 0 && len(evs) > 0 {
			return a.reportWait(ctx, evs, only)
		}
		if prev, err = a.state.loadSnapshot(a.cursor); err != nil {
			return err
		}
	}
	started := time.Now()
	deadline := started.Add(*timeout)
	for {
		cur, err := a.client.sessions(ctx, false)
		if err != nil {
			return err
		}
		evs := a.diffSessions(prev, cur)
		var hits []event
		for _, ev := range evs {
			if len(only) > 0 && !only[ev.Session.ID] {
				continue
			}
			if ev.Attention || (*anyChange && ev.Kind != "") {
				hits = append(hits, ev)
			}
		}
		if len(hits) > 0 {
			if err := a.saveWaitSnapshot(prev, cur, only); err != nil {
				return err
			}
			return a.reportWait(ctx, evs, only)
		}
		if time.Now().After(deadline) {
			if a.json {
				return a.printJSON(map[string]any{"events": []event{}, "timedOut": true})
			}
			var running []string
			shorts := shortIDs(cur)
			for _, s := range cur {
				if s.Status == model.StatusRunning && !a.isSelf(s) && (len(only) == 0 || only[s.ID]) {
					running = append(running, shorts[s.ID])
				}
			}
			fmt.Fprintf(a.out, "nothing needs you yet (waited %s).", time.Since(started).Round(time.Second))
			if len(running) > 0 {
				fmt.Fprintf(a.out, " still running: %s.", strings.Join(running, " "))
			}
			fmt.Fprintln(a.out, " Run hc wait again.")
			return nil
		}
		time.Sleep(min(pollInterval, time.Until(deadline)+time.Millisecond))
	}
}

// saveWaitSnapshot moves the cursor past what wait reports. Waiting on
// given sessions moves it for those sessions only, so changes elsewhere
// stay for the next "hc changes" or "hc wait".
func (a *app) saveWaitSnapshot(prev *snapshot, cur []model.Session, only map[string]bool) error {
	if len(only) == 0 {
		return a.state.saveSnapshot(a.cursor, cur, time.Now())
	}
	var merged []model.Session
	for id, old := range prev.Sessions {
		if !only[id] {
			merged = append(merged, model.Session{ID: id, Status: old.Status, UpdatedAt: old.UpdatedAt, LastMessage: old.LastMessage})
		}
	}
	for _, s := range cur {
		if only[s.ID] {
			merged = append(merged, s)
		}
	}
	return a.state.writeSnapshot(a.cursor, merged, prev.TakenAt)
}

func (a *app) reportWait(ctx context.Context, evs []event, only map[string]bool) error {
	if len(only) > 0 {
		evs = filterEvents(evs, only)
	}
	for i := range evs {
		if evs[i].Attention && needsYou(evs[i].Session.Status) {
			if _, msgs, err := a.client.messages(ctx, evs[i].Session.ID); err == nil {
				evs[i].Pending = pendingInteractions(msgs)
			}
		}
	}
	if a.json {
		return a.printJSON(map[string]any{"events": nonNil(evs), "timedOut": false})
	}
	a.printEvents(a.out, evs)
	return nil
}

func filterEvents(evs []event, only map[string]bool) []event {
	var out []event
	for _, ev := range evs {
		if only[ev.Session.ID] {
			out = append(out, ev)
		}
	}
	return out
}

// waitSettled waits for a session to finish the turn that began after
// before: it has been seen running or got newer, and is no longer running.
func (a *app) waitSettled(ctx context.Context, id string, before time.Time, timeout time.Duration) (model.Session, bool, error) {
	deadline := time.Now().Add(timeout)
	sawRunning := false
	for {
		s, err := a.client.session(ctx, id)
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.status == http.StatusNotFound {
			err = nil // not listed yet right after a start
		}
		if err != nil {
			return s, false, err
		}
		if s.Status == model.StatusRunning {
			sawRunning = true
		} else if s.ID != "" && (sawRunning || s.UpdatedAt.After(before)) {
			return s, true, nil
		}
		if time.Now().After(deadline) {
			return s, false, nil
		}
		time.Sleep(min(pollInterval, time.Until(deadline)+time.Millisecond))
	}
}
