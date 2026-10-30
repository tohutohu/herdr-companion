package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"crypto/rand"
	"encoding/hex"
)

// Resident tasks (常駐) are standing instructions the Gateway runs on a
// schedule or on request. hc manages them; it never runs them itself. The
// types here mirror the Gateway's JSON without importing its database code.

type resTask struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Instruction string          `json:"instruction"`
	Provider    string          `json:"provider"`
	Model       string          `json:"model,omitempty"`
	Effort      string          `json:"effort,omitempty"`
	Cwd         string          `json:"cwd"`
	Schedule    resSchedule     `json:"schedule"`
	Permissions json.RawMessage `json:"permissions"`
	Notify      json.RawMessage `json:"notify"`
	Limits      json.RawMessage `json:"limits"`
	Enabled     bool            `json:"enabled"`
	PausedUntil *time.Time      `json:"pausedUntil"`
	Version     int64           `json:"version"`
	State       string          `json:"state"`
	NextRunAt   *time.Time      `json:"nextRunAt"`
	LastSuccess *time.Time      `json:"lastSuccessAt"`
	Unread      int             `json:"unread"`
	Attention   string          `json:"attention"`
	LastRun     *struct {
		ID, Status, Outcome, Summary, Error string
	} `json:"lastRun"`
	LatestReport *struct {
		Summary string    `json:"summary"`
		At      time.Time `json:"at"`
	} `json:"latestReport"`
	PendingQuestion *resQuestion `json:"pendingQuestion"`
}

type resSchedule struct {
	Kind     string     `json:"kind"`
	At       *time.Time `json:"at,omitempty"`
	Time     string     `json:"time,omitempty"`
	Days     []string   `json:"days,omitempty"`
	Cron     string     `json:"cron,omitempty"`
	Timezone string     `json:"timezone,omitempty"`
}

func (s resSchedule) String() string {
	switch s.Kind {
	case "once":
		if s.At != nil {
			return "once " + s.At.Local().Format("2006-01-02 15:04")
		}
	case "daily":
		return "daily " + s.Time + " " + s.Timezone
	case "weekly":
		return "weekly " + strings.Join(s.Days, ",") + " " + s.Time + " " + s.Timezone
	case "cron":
		return "cron \"" + s.Cron + "\" " + s.Timezone
	}
	return s.Kind
}

type resQuestion struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Options   []string  `json:"options"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type resRun struct {
	ID             string     `json:"id"`
	Status         string     `json:"status"`
	Triggers       []string   `json:"triggers"`
	Reason         string     `json:"reason"`
	QueuedAt       time.Time  `json:"queuedAt"`
	StartedAt      *time.Time `json:"startedAt"`
	EndedAt        *time.Time `json:"endedAt"`
	Outcome        string     `json:"outcome"`
	Summary        string     `json:"summary"`
	Report         string     `json:"report"`
	ErrorKind      string     `json:"errorKind"`
	Error          string     `json:"error"`
	Model          string     `json:"model"`
	Effort         string     `json:"effort"`
	Warnings       []string   `json:"warnings"`
	NotifyDecision string     `json:"notifyDecision"`
	Conversation   string     `json:"conversation"`
	SessionID      string     `json:"sessionId"`
	Attempts       []struct {
		Phase    string `json:"phase"`
		Provider string `json:"provider"`
		NativeID string `json:"nativeId"`
		Error    string `json:"error"`
	} `json:"attempts"`
}

type resMessage struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Kind      string    `json:"kind"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
}

func (a *app) cmdResident(args []string) error {
	if len(args) == 0 {
		return usagef("resident needs a subcommand: ls, show, create, update, pause, resume, run, runs, log, cancel, dismiss, promote, send, answer, messages, memory, preview, delete")
	}
	sub, rest := args[0], args[1:]
	cmds := map[string]func([]string) error{
		"ls": a.resLs, "show": a.resShow, "create": a.resCreate, "update": a.resUpdate,
		"pause": a.resEnabled(false), "resume": a.resEnabled(true), "run": a.resRunNow, "runs": a.resRuns,
		"log": a.resLog, "cancel": a.resRunAction("cancel"), "dismiss": a.resRunAction("dismiss"), "promote": a.resPromote,
		"send": a.resSend, "answer": a.resAnswer, "messages": a.resMessages, "memory": a.resMemory,
		"preview": a.resPreview, "delete": a.resDelete,
	}
	run, ok := cmds[sub]
	if !ok {
		return usagef("unknown resident subcommand %q", sub)
	}
	return run(rest)
}

func (a *app) residentTasks(ctx context.Context) ([]resTask, error) {
	var res struct {
		Tasks []resTask `json:"tasks"`
	}
	err := a.client.do(ctx, http.MethodGet, "/v1/resident/tasks", nil, &res)
	var ae *apiError
	if errors.As(err, &ae) && ae.status == http.StatusNotFound {
		return nil, errors.New("this Gateway has no resident tasks (update the Gateway)")
	}
	return res.Tasks, err
}

// resolveTask finds a task by id, unique id prefix or exact name.
func (a *app) resolveTask(ctx context.Context, ref string) (resTask, error) {
	tasks, err := a.residentTasks(ctx)
	if err != nil {
		return resTask{}, err
	}
	var found []resTask
	for _, t := range tasks {
		if t.ID == ref || t.Name == ref {
			return t, nil
		}
		if strings.HasPrefix(t.ID, ref) || strings.HasPrefix(strings.TrimPrefix(t.ID, "task_"), ref) {
			found = append(found, t)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return resTask{}, fmt.Errorf("no resident task %q (hc resident ls)", ref)
	}
	return resTask{}, fmt.Errorf("%q matches %d tasks; give more of the id", ref, len(found))
}

func shortTask(id string) string { return clipID(strings.TrimPrefix(id, "task_"), 8) }
func shortRun(id string) string  { return strings.TrimPrefix(id, "run_") }

func needsAttention(t resTask) bool {
	return t.State == "waiting_user" || t.State == "needs_attention"
}

func (a *app) resLs(args []string) error {
	if _, err := parseFlags(flag.NewFlagSet("resident ls", flag.ContinueOnError), args); err != nil {
		return err
	}
	tasks, err := a.residentTasks(context.Background())
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(tasks)
	}
	if len(tasks) == 0 {
		fmt.Fprintln(a.out, "no resident tasks (hc resident create --help)")
		return nil
	}
	for _, t := range tasks {
		mark := " "
		if needsAttention(t) {
			mark = "!"
		}
		next := "-"
		if t.NextRunAt != nil {
			next = clock(*t.NextRunAt)
		}
		line := fmt.Sprintf("%s %s %-15s next %-11s %-6s %s", mark, shortTask(t.ID), t.State, next, t.Provider, t.Name)
		if t.Unread > 0 {
			line += fmt.Sprintf("  (%d unread)", t.Unread)
		}
		fmt.Fprintln(a.out, line)
	}
	return nil
}

func (a *app) resShow(args []string) error {
	pos, err := parseFlags(flag.NewFlagSet("resident show", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("resident show needs a TASK")
	}
	ctx := context.Background()
	t, err := a.resolveTask(ctx, pos[0])
	if err != nil {
		return err
	}
	var detail struct {
		Task     resTask     `json:"task"`
		Upcoming []time.Time `json:"upcoming"`
		Memory   struct {
			Version int64                      `json:"version"`
			Summary string                     `json:"summary"`
			State   map[string]json.RawMessage `json:"state"`
		} `json:"memory"`
		Recent []resRun `json:"recentRuns"`
	}
	var raw json.RawMessage
	if err := a.client.do(ctx, http.MethodGet, "/v1/resident/tasks/"+url.PathEscape(t.ID), nil, &raw); err != nil {
		return err
	}
	if a.json {
		_, err := a.out.Write(append(raw, '\n'))
		return err
	}
	json.Unmarshal(raw, &detail)
	d := detail.Task
	fmt.Fprintf(a.out, "%s  %s  [%s]\n", d.ID, d.Name, d.State)
	if d.Attention != "" {
		fmt.Fprintln(a.out, "attention:", d.Attention)
	}
	fmt.Fprintf(a.out, "agent: %s %s %s  cwd: %s\n", d.Provider, firstNonEmpty(d.Model, "(default model)"), d.Effort, d.Cwd)
	fmt.Fprintf(a.out, "schedule: %s  enabled: %v\n", d.Schedule, d.Enabled)
	if d.PausedUntil != nil {
		fmt.Fprintln(a.out, "paused until:", d.PausedUntil.Local().Format("2006-01-02 15:04"))
	}
	var next []string
	for _, u := range detail.Upcoming {
		next = append(next, u.Local().Format("01-02 15:04"))
	}
	if len(next) > 0 {
		fmt.Fprintln(a.out, "next:", strings.Join(next, ", "))
	}
	fmt.Fprintf(a.out, "permissions: %s  notify: %s  limits: %s\n", d.Permissions, d.Notify, d.Limits)
	fmt.Fprintf(a.out, "instruction:\n  %s\n", strings.ReplaceAll(d.Instruction, "\n", "\n  "))
	if detail.Memory.Summary != "" || len(detail.Memory.State) > 0 {
		fmt.Fprintf(a.out, "memory (v%d): %s\n", detail.Memory.Version, detail.Memory.Summary)
		if len(detail.Memory.State) > 0 {
			b, _ := json.Marshal(detail.Memory.State)
			fmt.Fprintf(a.out, "  state: %s\n", b)
		}
	}
	if q := d.PendingQuestion; q != nil {
		fmt.Fprintf(a.out, "! question: %s\n  answer: hc resident answer %s \"...\"\n", q.Text, shortTask(d.ID))
	}
	if len(detail.Recent) > 0 {
		fmt.Fprintln(a.out, "recent runs:")
		for _, r := range detail.Recent {
			fmt.Fprintln(a.out, "  "+runLine(r))
		}
	}
	return nil
}

func runLine(r resRun) string {
	at := r.QueuedAt
	if r.StartedAt != nil {
		at = *r.StartedAt
	}
	s := fmt.Sprintf("%s %s %-20s %s", shortRun(r.ID), clock(at), r.Status, strings.Join(r.Triggers, "+"))
	if r.Outcome != "" {
		s += " " + r.Outcome
	}
	if r.Summary != "" {
		s += " · " + clipLine(r.Summary, 100)
	} else if r.Error != "" {
		s += " · " + clipLine(r.Error, 100)
	} else if r.Reason != "" && r.Status == "skipped" {
		s += " · " + clipLine(r.Reason, 100)
	}
	return s
}

func clipLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// taskFlags are the settings create and update share.
type taskFlags struct {
	fs                                                *flag.FlagSet
	file, name, provider, model, effort, cwd, instr   *string
	instrFile, daily, weekly, cron, once, tz, profile *string
	manual, trust, enable, disable                    *bool
	allow                                             *multiFlag
	notify                                            *string
	maxMinutes, maxRuns                               *int
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func newTaskFlags(name string) *taskFlags {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	f := &taskFlags{fs: fs, allow: &multiFlag{}}
	f.file = fs.String("file", "", "")
	f.name = fs.String("name", "", "")
	f.provider = fs.String("provider", "", "")
	f.model = fs.String("model", "", "")
	f.effort = fs.String("effort", "", "")
	f.cwd = fs.String("cwd", "", "")
	f.instr = fs.String("instruction", "", "")
	f.instrFile = fs.String("instruction-file", "", "")
	f.daily = fs.String("daily", "", "")
	f.weekly = fs.String("weekly", "", "")
	f.cron = fs.String("cron", "", "")
	f.once = fs.String("once", "", "")
	f.tz = fs.String("tz", "", "")
	f.profile = fs.String("profile", "", "")
	f.notify = fs.String("notify", "", "")
	f.manual = fs.Bool("manual", false, "")
	f.trust = fs.Bool("trust", false, "")
	f.enable = fs.Bool("enable", false, "")
	f.disable = fs.Bool("disable", false, "")
	f.maxMinutes = fs.Int("max-minutes", 0, "")
	f.maxRuns = fs.Int("max-runs-per-day", 0, "")
	fs.Var(f.allow, "allow", "")
	return f
}

// body builds the JSON fields the flags set, on top of --file.
func (f *taskFlags) body(set map[string]bool) (map[string]any, error) {
	m := map[string]any{}
	if *f.file != "" {
		b, err := readInput(*f.file)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, usagef("--file %s is not a JSON object: %v", *f.file, err)
		}
	}
	str := func(flagName, key string, v *string) {
		if set[flagName] {
			m[key] = *v
		}
	}
	str("name", "name", f.name)
	str("provider", "provider", f.provider)
	str("model", "model", f.model)
	str("effort", "effort", f.effort)
	if set["cwd"] {
		abs, err := filepath.Abs(*f.cwd)
		if err != nil {
			return nil, err
		}
		m["cwd"] = abs
	}
	str("instruction", "instruction", f.instr)
	if set["instruction-file"] {
		b, err := readInput(*f.instrFile)
		if err != nil {
			return nil, err
		}
		m["instruction"] = string(b)
	}
	var sched map[string]any
	n := 0
	for _, k := range []string{"daily", "weekly", "cron", "once", "manual"} {
		if set[k] {
			n++
		}
	}
	if n > 1 {
		return nil, usagef("give one of --daily, --weekly, --cron, --once, --manual")
	}
	switch {
	case set["daily"]:
		sched = map[string]any{"kind": "daily", "time": *f.daily}
	case set["weekly"]:
		days, at, ok := strings.Cut(*f.weekly, "@")
		if !ok {
			return nil, usagef("--weekly takes DAYS@HH:MM, e.g. mon,thu@07:00")
		}
		sched = map[string]any{"kind": "weekly", "time": at, "days": strings.Split(days, ",")}
	case set["cron"]:
		sched = map[string]any{"kind": "cron", "cron": *f.cron}
	case set["once"]:
		at, err := time.Parse(time.RFC3339, *f.once)
		if err != nil {
			return nil, usagef("--once takes an RFC 3339 time, e.g. 2026-11-01T07:00:00+09:00")
		}
		sched = map[string]any{"kind": "once", "at": at}
	case set["manual"]:
		sched = map[string]any{"kind": "manual"}
	}
	if sched != nil || set["tz"] {
		if sched == nil {
			if cur, ok := m["schedule"].(map[string]any); ok {
				sched = cur
			} else {
				return nil, usagef("--tz needs a schedule flag too")
			}
		}
		if set["tz"] {
			sched["timezone"] = *f.tz
		}
		m["schedule"] = sched
	}
	if set["profile"] || len(*f.allow) > 0 {
		perms, _ := m["permissions"].(map[string]any)
		if perms == nil {
			perms = map[string]any{}
		}
		if set["profile"] {
			perms["profile"] = *f.profile
		}
		if len(*f.allow) > 0 {
			perms["allowedTools"] = []string(*f.allow)
		}
		m["permissions"] = perms
	}
	if set["notify"] {
		m["notify"] = map[string]any{"mode": *f.notify, "onFailure": true, "onQuestion": true}
	}
	if set["max-minutes"] || set["max-runs-per-day"] {
		lim, _ := m["limits"].(map[string]any)
		if lim == nil {
			lim = map[string]any{}
		}
		if set["max-minutes"] {
			lim["maxRunMinutes"] = *f.maxMinutes
		}
		if set["max-runs-per-day"] {
			lim["maxRunsPerDay"] = *f.maxRuns
		}
		m["limits"] = lim
	}
	if set["trust"] {
		m["trust"] = true
	}
	if set["enable"] {
		m["enabled"] = true
	}
	if set["disable"] {
		m["enabled"] = false
	}
	return m, nil
}

// readInput reads a file, or stdin for "-".
func readInput(path string) ([]byte, error) {
	if path == "-" {
		var b strings.Builder
		buf := make([]byte, 64<<10)
		for {
			n, err := os.Stdin.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		return []byte(b.String()), nil
	}
	return os.ReadFile(path)
}

func flagsSet(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

func newRequestID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return "hc-" + hex.EncodeToString(b)
}

func (a *app) resCreate(args []string) error {
	f := newTaskFlags("resident create")
	reqID := f.fs.String("request-id", "", "")
	if _, err := parseFlags(f.fs, args); err != nil {
		return err
	}
	body, err := f.body(flagsSet(f.fs))
	if err != nil {
		return err
	}
	if _, ok := body["schedule"]; !ok {
		body["schedule"] = map[string]any{"kind": "manual"}
	}
	if _, ok := body["requestId"]; !ok {
		body["requestId"] = firstNonEmpty(*reqID, newRequestID())
	}
	var t resTask
	if err := a.client.do(context.Background(), http.MethodPost, "/v1/resident/tasks", body, &t); err != nil {
		return residentErr(err)
	}
	if a.json {
		return a.printJSON(t)
	}
	next := "no automatic run"
	if t.NextRunAt != nil {
		next = "next run " + t.NextRunAt.Local().Format("2006-01-02 15:04")
	}
	fmt.Fprintf(a.out, "created %s %q (%s); %s\n", shortTask(t.ID), t.Name, t.Schedule, next)
	return nil
}

// residentErr adds what to do for the errors a caller can fix.
func residentErr(err error) error {
	var ae *apiError
	if errors.As(err, &ae) {
		switch {
		case ae.status == http.StatusConflict && strings.Contains(ae.message, "never trusted"):
			return fmt.Errorf("%s\n(add --trust after checking the folder's .claude/ and .codex/ settings)", ae.message)
		case ae.status == http.StatusConflict:
			return fmt.Errorf("conflict: %s", ae.message)
		case ae.status == http.StatusBadRequest:
			return fmt.Errorf("refused: %s", ae.message)
		case ae.status == http.StatusNotFound:
			return fmt.Errorf("not found: %s", ae.message)
		}
	}
	return err
}

func (a *app) resUpdate(args []string) error {
	f := newTaskFlags("resident update")
	pos, err := parseFlags(f.fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("resident update needs a TASK")
	}
	ctx := context.Background()
	t, err := a.resolveTask(ctx, pos[0])
	if err != nil {
		return err
	}
	body, err := f.body(flagsSet(f.fs))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return usagef("resident update: nothing to change")
	}
	if _, ok := body["expectedVersion"]; !ok {
		body["expectedVersion"] = t.Version
	}
	var out resTask
	if err := a.client.do(ctx, http.MethodPatch, "/v1/resident/tasks/"+url.PathEscape(t.ID), body, &out); err != nil {
		return residentErr(err)
	}
	if a.json {
		return a.printJSON(out)
	}
	fmt.Fprintf(a.out, "updated %s %q (version %d)\n", shortTask(out.ID), out.Name, out.Version)
	return nil
}

func (a *app) resEnabled(enabled bool) func([]string) error {
	return func(args []string) error {
		fs := flag.NewFlagSet("resident pause", flag.ContinueOnError)
		var forDur *time.Duration
		var until *string
		if !enabled {
			forDur = fs.Duration("for", 0, "")
			until = fs.String("until", "", "")
		}
		pos, err := parseFlags(fs, args)
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return usagef("needs a TASK")
		}
		body := map[string]any{}
		if !enabled {
			switch {
			case *forDur != 0 && *until != "":
				return usagef("use --for or --until, not both")
			case *forDur < 0:
				return usagef("--for must be positive")
			case *forDur > 0:
				body["until"] = time.Now().Add(*forDur).UTC().Format(time.RFC3339)
			case *until != "":
				at, err := time.Parse(time.RFC3339, *until)
				if err != nil {
					return usagef("--until needs an RFC 3339 time such as 2026-11-03T09:00:00+09:00")
				}
				body["until"] = at.Format(time.RFC3339)
			}
		}
		ctx := context.Background()
		t, err := a.resolveTask(ctx, pos[0])
		if err != nil {
			return err
		}
		path := "/v1/resident/tasks/" + url.PathEscape(t.ID) + "/pause"
		if enabled {
			path = "/v1/resident/tasks/" + url.PathEscape(t.ID) + "/resume"
		}
		var out resTask
		if err := a.client.do(ctx, http.MethodPost, path, body, &out); err != nil {
			return residentErr(err)
		}
		if a.json {
			return a.printJSON(out)
		}
		if body["until"] != nil && out.PausedUntil == nil {
			return fmt.Errorf("the gateway paused %s without an end: it predates timed pauses; resume it yourself with hc resident resume", shortTask(out.ID))
		}
		if out.PausedUntil != nil {
			fmt.Fprintf(a.out, "%s %s until %s\n", shortTask(out.ID), out.State, out.PausedUntil.Local().Format("2006-01-02 15:04"))
			return nil
		}
		fmt.Fprintf(a.out, "%s %s\n", shortTask(out.ID), out.State)
		return nil
	}
}

type postResult struct {
	Run    resRun `json:"run"`
	Merged bool   `json:"merged"`
}

func (a *app) resRunNow(args []string) error {
	fs := flag.NewFlagSet("resident run", flag.ContinueOnError)
	reqID := fs.String("request-id", "", "")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("resident run needs a TASK")
	}
	ctx := context.Background()
	t, err := a.resolveTask(ctx, pos[0])
	if err != nil {
		return err
	}
	var res postResult
	if err := a.client.do(ctx, http.MethodPost, "/v1/resident/tasks/"+url.PathEscape(t.ID)+"/runs",
		map[string]any{"requestId": firstNonEmpty(*reqID, newRequestID())}, &res); err != nil {
		return residentErr(err)
	}
	if a.json {
		return a.printJSON(res)
	}
	how := "queued"
	if res.Merged {
		how = "joined the queued run"
	}
	fmt.Fprintf(a.out, "%s run %s (hc resident log %s)\n", how, shortRun(res.Run.ID), shortRun(res.Run.ID))
	return nil
}

func (a *app) resRuns(args []string) error {
	fs := flag.NewFlagSet("resident runs", flag.ContinueOnError)
	limit := fs.Int("limit", 20, "")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("resident runs needs a TASK")
	}
	ctx := context.Background()
	t, err := a.resolveTask(ctx, pos[0])
	if err != nil {
		return err
	}
	var res struct {
		Runs []resRun `json:"runs"`
	}
	if err := a.client.do(ctx, http.MethodGet, fmt.Sprintf("/v1/resident/tasks/%s/runs?limit=%d", url.PathEscape(t.ID), *limit), nil, &res); err != nil {
		return residentErr(err)
	}
	if a.json {
		return a.printJSON(res.Runs)
	}
	for _, r := range res.Runs {
		fmt.Fprintln(a.out, runLine(r))
	}
	return nil
}

func runPath(id string) string {
	if !strings.HasPrefix(id, "run_") {
		id = "run_" + id
	}
	return "/v1/resident/runs/" + url.PathEscape(id)
}

func (a *app) resLog(args []string) error {
	pos, err := parseFlags(flag.NewFlagSet("resident log", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("resident log needs a RUN id")
	}
	var raw json.RawMessage
	if err := a.client.do(context.Background(), http.MethodGet, runPath(pos[0]), nil, &raw); err != nil {
		return residentErr(err)
	}
	if a.json {
		_, err := a.out.Write(append(raw, '\n'))
		return err
	}
	var r resRun
	json.Unmarshal(raw, &r)
	fmt.Fprintln(a.out, runLine(r))
	fmt.Fprintf(a.out, "model: %s %s  notify: %s\n", firstNonEmpty(r.Model, "-"), r.Effort, firstNonEmpty(r.NotifyDecision, "-"))
	if r.Error != "" {
		fmt.Fprintf(a.out, "error (%s): %s\n", r.ErrorKind, r.Error)
	}
	for _, w := range r.Warnings {
		fmt.Fprintln(a.out, "warning:", w)
	}
	for _, at := range r.Attempts {
		fmt.Fprintf(a.out, "attempt: %s %s %s %s\n", at.Provider, at.Phase, at.NativeID, at.Error)
	}
	if r.Report != "" {
		fmt.Fprintf(a.out, "report:\n%s\n", r.Report)
	}
	if r.Status == "needs_reconciliation" {
		fmt.Fprintf(a.out, "The outcome is unknown. Check what it did, then: hc resident dismiss %s\n", shortRun(r.ID))
	}
	switch r.Conversation {
	case "kept":
		fmt.Fprintf(a.out, "Continue this run's conversation as a session: hc resident promote %s\n", shortRun(r.ID))
	case "promoted":
		fmt.Fprintf(a.out, "session: %s\n", r.SessionID)
	}
	return nil
}

// resPromote makes a run's conversation a session (resumed, listed by hc ls).
func (a *app) resPromote(args []string) error {
	pos, err := parseFlags(flag.NewFlagSet("resident promote", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("resident promote needs a RUN id")
	}
	var res struct {
		SessionID string `json:"sessionId"`
	}
	if err := a.client.do(context.Background(), http.MethodPost, runPath(pos[0])+"/promote", map[string]any{}, &res); err != nil {
		return residentErr(err)
	}
	if a.json {
		return a.printJSON(res)
	}
	fmt.Fprintf(a.out, "session %s\n", res.SessionID)
	return nil
}

func (a *app) resRunAction(action string) func([]string) error {
	return func(args []string) error {
		pos, err := parseFlags(flag.NewFlagSet("resident "+action, flag.ContinueOnError), args)
		if err != nil {
			return err
		}
		if len(pos) != 1 {
			return usagef("resident %s needs a RUN id", action)
		}
		var r resRun
		if err := a.client.do(context.Background(), http.MethodPost, runPath(pos[0])+"/"+action, map[string]any{}, &r); err != nil {
			return residentErr(err)
		}
		if a.json {
			return a.printJSON(r)
		}
		fmt.Fprintln(a.out, runLine(r))
		return nil
	}
}

func (a *app) resSend(args []string) error {
	fs := flag.NewFlagSet("resident send", flag.ContinueOnError)
	file := fs.String("file", "", "")
	reqID := fs.String("request-id", "", "")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 || (len(pos) < 2 && *file == "") {
		return usagef("resident send needs a TASK and TEXT (or --file)")
	}
	text := strings.Join(pos[1:], " ")
	if *file != "" {
		b, err := readInput(*file)
		if err != nil {
			return err
		}
		text = string(b)
	}
	ctx := context.Background()
	t, err := a.resolveTask(ctx, pos[0])
	if err != nil {
		return err
	}
	var res postResult
	if err := a.client.do(ctx, http.MethodPost, "/v1/resident/tasks/"+url.PathEscape(t.ID)+"/messages",
		map[string]any{"text": text, "requestId": firstNonEmpty(*reqID, newRequestID())}, &res); err != nil {
		return residentErr(err)
	}
	if a.json {
		return a.printJSON(res)
	}
	fmt.Fprintf(a.out, "sent; run %s will take it (hc resident messages %s)\n", shortRun(res.Run.ID), shortTask(t.ID))
	return nil
}

func (a *app) resAnswer(args []string) error {
	fs := flag.NewFlagSet("resident answer", flag.ContinueOnError)
	reqID := fs.String("request-id", "", "")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		return usagef("resident answer needs a TASK and TEXT")
	}
	ctx := context.Background()
	t, err := a.resolveTask(ctx, pos[0])
	if err != nil {
		return err
	}
	if t.PendingQuestion == nil {
		return fmt.Errorf("%s has no open question", shortTask(t.ID))
	}
	var res postResult
	if err := a.client.do(ctx, http.MethodPost, "/v1/resident/questions/"+url.PathEscape(t.PendingQuestion.ID)+"/answers",
		map[string]any{"text": strings.Join(pos[1:], " "), "requestId": firstNonEmpty(*reqID, newRequestID())}, &res); err != nil {
		return residentErr(err)
	}
	if a.json {
		return a.printJSON(res)
	}
	fmt.Fprintf(a.out, "answered; run %s continues from where it stopped\n", shortRun(res.Run.ID))
	return nil
}

func (a *app) resMessages(args []string) error {
	fs := flag.NewFlagSet("resident messages", flag.ContinueOnError)
	limit := fs.Int("limit", 20, "")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("resident messages needs a TASK")
	}
	ctx := context.Background()
	t, err := a.resolveTask(ctx, pos[0])
	if err != nil {
		return err
	}
	var res struct {
		Messages []resMessage `json:"messages"`
	}
	if err := a.client.do(ctx, http.MethodGet, fmt.Sprintf("/v1/resident/tasks/%s/messages?limit=%d", url.PathEscape(t.ID), *limit), nil, &res); err != nil {
		return residentErr(err)
	}
	if a.json {
		return a.printJSON(res.Messages)
	}
	for _, m := range res.Messages {
		fmt.Fprintf(a.out, "── %s %s %s\n%s\n", clock(m.CreatedAt), m.Role, m.Kind, strings.TrimSpace(m.Text))
	}
	return nil
}

func (a *app) resMemory(args []string) error {
	fs := flag.NewFlagSet("resident memory", flag.ContinueOnError)
	set := fs.String("set", "", "")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("resident memory needs a TASK")
	}
	ctx := context.Background()
	t, err := a.resolveTask(ctx, pos[0])
	if err != nil {
		return err
	}
	path := "/v1/resident/tasks/" + url.PathEscape(t.ID) + "/memory"
	var mem struct {
		Version int64                      `json:"version"`
		Summary string                     `json:"summary"`
		State   map[string]json.RawMessage `json:"state"`
	}
	if err := a.client.do(ctx, http.MethodGet, path, nil, &mem); err != nil {
		return residentErr(err)
	}
	if *set != "" {
		b, err := readInput(*set)
		if err != nil {
			return err
		}
		var up map[string]any
		if err := json.Unmarshal(b, &up); err != nil {
			return usagef("--set takes a JSON object {summary?, state?}: %v", err)
		}
		up["expectedVersion"] = mem.Version
		if err := a.client.do(ctx, http.MethodPut, path, up, &mem); err != nil {
			return residentErr(err)
		}
	}
	if a.json {
		return a.printJSON(mem)
	}
	fmt.Fprintf(a.out, "memory v%d\nsummary: %s\n", mem.Version, mem.Summary)
	b, _ := json.MarshalIndent(mem.State, "", "  ")
	fmt.Fprintf(a.out, "state: %s\n", b)
	return nil
}

func (a *app) resPreview(args []string) error {
	f := newTaskFlags("resident preview")
	if _, err := parseFlags(f.fs, args); err != nil {
		return err
	}
	body, err := f.body(flagsSet(f.fs))
	if err != nil {
		return err
	}
	sched, ok := body["schedule"]
	if !ok {
		return usagef("resident preview needs a schedule flag (--daily, --weekly, --cron, --once)")
	}
	var res struct {
		Next []time.Time `json:"next"`
	}
	if err := a.client.do(context.Background(), http.MethodPost, "/v1/resident/schedule/preview", map[string]any{"schedule": sched, "count": 3}, &res); err != nil {
		return residentErr(err)
	}
	if a.json {
		return a.printJSON(res)
	}
	if len(res.Next) == 0 {
		fmt.Fprintln(a.out, "no upcoming run")
	}
	for _, t := range res.Next {
		fmt.Fprintln(a.out, t.Local().Format("2006-01-02 (Mon) 15:04 MST"))
	}
	return nil
}

func (a *app) resDelete(args []string) error {
	fs := flag.NewFlagSet("resident delete", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("resident delete needs a TASK")
	}
	if !*yes {
		return usagef("resident delete stops the task for good; add --yes")
	}
	ctx := context.Background()
	t, err := a.resolveTask(ctx, pos[0])
	if err != nil {
		return err
	}
	if err := a.client.do(ctx, http.MethodDelete, "/v1/resident/tasks/"+url.PathEscape(t.ID), nil, nil); err != nil {
		return residentErr(err)
	}
	fmt.Fprintf(a.out, "deleted %s %q\n", shortTask(t.ID), t.Name)
	return nil
}
