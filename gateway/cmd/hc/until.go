package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// untilState is a name "hc until --for" accepts and the statuses it means.
type untilState struct {
	name     string
	statuses []model.Status
	// turnOver makes idle and completed count only when the agent has
	// answered the latest prompt (see promptUnanswered).
	turnOver bool
}

var untilStates = []untilState{
	{name: "input", statuses: []model.Status{model.StatusWaitingInput}},
	{name: "approval", statuses: []model.Status{model.StatusWaitingApproval}},
	{name: "needs-you", statuses: []model.Status{model.StatusWaitingInput, model.StatusWaitingApproval}},
	{name: "done", statuses: []model.Status{model.StatusCompleted, model.StatusIdle}, turnOver: true},
	{name: "failed", statuses: []model.Status{model.StatusFailed}},
	{name: "offline", statuses: []model.Status{model.StatusOffline}},
	{name: "stopped", statuses: []model.Status{model.StatusWaitingInput, model.StatusWaitingApproval,
		model.StatusCompleted, model.StatusIdle, model.StatusFailed, model.StatusOffline}, turnOver: true},
	{name: "running", statuses: []model.Status{model.StatusRunning}},
}

var rawStatuses = []model.Status{
	model.StatusRunning, model.StatusWaitingInput, model.StatusWaitingApproval, model.StatusCompleted,
	model.StatusIdle, model.StatusFailed, model.StatusOffline,
}

// untilTarget maps each status to wait for to whether it also needs the
// turn to be over. A raw status name matches exactly and wins over a group.
type untilTarget map[model.Status]bool

func parseUntilTarget(specs []string) (untilTarget, error) {
	if len(specs) == 0 {
		specs = []string{"stopped"}
	}
	tg := untilTarget{}
	exact := map[model.Status]bool{}
	for _, spec := range specs {
		for _, name := range strings.Split(spec, ",") {
			name = strings.ToLower(strings.TrimSpace(name))
			if name == "" {
				continue
			}
			found := false
			for _, st := range untilStates {
				if st.name == name {
					for _, s := range st.statuses {
						tg[s] = tg[s] || st.turnOver
					}
					found = true
				}
			}
			for _, s := range rawStatuses {
				if string(s) == name {
					exact[s], found = true, true
				}
			}
			if !found {
				var names []string
				for _, st := range untilStates {
					names = append(names, st.name)
				}
				return nil, usagef("unknown state %q for --for; use %s, or a status (see \"hc help states\")", name, strings.Join(names, " "))
			}
		}
	}
	for s := range exact {
		tg[s] = false
	}
	if len(tg) == 0 {
		return nil, usagef("--for needs a state (see \"hc help until\")")
	}
	return tg, nil
}

// freshPrompt is how long a prompt the agent has not answered keeps an idle
// session from counting as done: the prompt lands in the transcript before
// the agent starts working on it.
const freshPrompt = time.Minute

// promptUnanswered reports whether the newest message is a recent prompt,
// so the idle status is from before the turn that prompt starts. An old one
// (an interrupted turn) does not hold the wait forever.
func (a *app) promptUnanswered(ctx context.Context, id string) bool {
	_, msgs, err := a.client.messages(ctx, id)
	if err != nil || len(msgs) == 0 {
		return false
	}
	last := msgs[len(msgs)-1]
	return last.Role == model.RoleUser && time.Since(last.Timestamp) < freshPrompt
}

func (a *app) untilReached(ctx context.Context, s model.Session, tg untilTarget) bool {
	turnOver, ok := tg[s.Status]
	if !ok {
		return false
	}
	if turnOver && (s.Status == model.StatusIdle || s.Status == model.StatusCompleted) {
		return !a.promptUnanswered(ctx, s.ID)
	}
	return true
}

type untilSession struct {
	Session model.Session       `json:"session"`
	Short   string              `json:"short"`
	Reached bool                `json:"reached"`
	Gone    bool                `json:"gone,omitempty"`
	Pending []model.Interaction `json:"pending,omitempty"`
}

func (a *app) cmdUntil(args []string) error {
	fs := flag.NewFlagSet("until", flag.ContinueOnError)
	var forList stringList
	fs.Var(&forList, "for", "states to wait for, comma separated (default stopped)")
	all := fs.Bool("all", false, "with several IDs, wait until every one is there")
	timeout := fs.Duration("timeout", 110*time.Second, "give up after this long (e.g. 9m, 1h)")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return usagef("until needs one or more session IDs")
	}
	tg, err := parseUntilTarget(forList)
	if err != nil {
		return err
	}
	ctx := context.Background()
	var ids []string
	known := map[string]model.Session{}
	for _, ref := range pos {
		s, err := a.resolve(ctx, ref)
		if err != nil {
			return err
		}
		if a.isSelf(s) {
			return fmt.Errorf("%s is this session itself; it cannot wait for itself", ref)
		}
		if _, dup := known[s.ID]; !dup {
			ids = append(ids, s.ID)
		}
		known[s.ID] = s
	}
	again := "hc until " + strings.Join(pos, " ")
	if len(forList) > 0 {
		again += " --for " + strings.Join(forList, ",")
	}
	if *all {
		again += " --all"
	}
	again += " --timeout " + timeout.String()
	started := time.Now()
	deadline := started.Add(*timeout)
	for first := true; ; first = false {
		cur, err := a.client.sessions(ctx, false)
		if err != nil {
			return err
		}
		shorts := shortIDs(cur)
		listed := map[string]model.Session{}
		for _, s := range cur {
			listed[s.ID] = s
		}
		var rows []untilSession
		reached := 0
		for _, id := range ids {
			s, ok := listed[id]
			r := untilSession{Session: s, Short: shorts[id]}
			if !ok {
				// Archived or aged out of the list: it is not coming back.
				r.Session, r.Short, r.Gone = known[id], clipID(nativeID(id), 8), true
				r.Session.Status = model.StatusOffline
			}
			if r.Reached = a.untilReached(ctx, r.Session, tg); r.Reached {
				reached++
			}
			rows = append(rows, r)
		}
		if (*all && reached == len(ids)) || (!*all && reached > 0) {
			if err := a.moveCursorFor(ids, cur); err != nil {
				return err
			}
			return a.reportUntil(ctx, rows, started, first, false, again)
		}
		if time.Now().After(deadline) {
			if err := a.reportUntil(ctx, rows, started, false, true, again); err != nil {
				return err
			}
			return exitStatus(3)
		}
		time.Sleep(min(pollInterval, time.Until(deadline)+time.Millisecond))
	}
}

// moveCursorFor marks what until reported as seen, so a later wait or
// changes does not report the same change again. Without a cursor yet
// there is nothing to move.
func (a *app) moveCursorFor(ids []string, cur []model.Session) error {
	prev, err := a.state.loadSnapshot(a.cursor)
	if err != nil || prev == nil {
		return err
	}
	only := map[string]bool{}
	for _, id := range ids {
		only[id] = true
	}
	return a.saveWaitSnapshot(prev, cur, only)
}

// reportUntil prints where each session is; again is the command that
// keeps waiting after a timeout.
func (a *app) reportUntil(ctx context.Context, rows []untilSession, started time.Time, already, timedOut bool, again string) error {
	waited := time.Since(started).Round(time.Second)
	for i := range rows {
		if rows[i].Reached && needsYou(rows[i].Session.Status) {
			if _, msgs, err := a.client.messages(ctx, rows[i].Session.ID); err == nil {
				rows[i].Pending = pendingInteractions(msgs)
			}
		}
	}
	if a.json {
		return a.printJSON(map[string]any{"sessions": rows, "reached": !timedOut, "timedOut": timedOut, "waitedSeconds": int(waited.Seconds())})
	}
	w := a.out
	switch {
	case timedOut:
		fmt.Fprintf(w, "timed out after %s:\n", waited)
	case already:
		fmt.Fprintln(w, "already there:")
	default:
		fmt.Fprintf(w, "reached after %s:\n", waited)
	}
	var next []string
	for _, r := range rows {
		line := a.sessionLine(r.Session, r.Short)
		if r.Gone {
			line += "  (gone: archived or too old)"
		}
		if !r.Reached {
			line += "  (not yet)"
		}
		fmt.Fprintln(w, line)
		if !r.Reached {
			continue
		}
		next = append(next, r.Short)
		for _, ia := range r.Pending {
			out := &strings.Builder{}
			renderInteraction(out, ia, r.Short)
			fmt.Fprintln(w, indent(out.String(), "    "))
		}
		if len(r.Pending) == 0 && r.Session.LastMessage != "" {
			fmt.Fprintf(w, "    last: %s\n", clip(oneLine(r.Session.LastMessage), 300))
		}
	}
	if timedOut {
		fmt.Fprintf(w, "continue: %s\n", again)
		return nil
	}
	fmt.Fprintf(w, "next: hc read %s\n", strings.Join(next, "   hc read "))
	return nil
}
