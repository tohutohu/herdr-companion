package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

// textArg is the prompt given as arguments, or read from stdin for "-".
func (a *app) textArg(words []string) (string, error) {
	if len(words) == 1 && words[0] == "-" {
		b, err := io.ReadAll(a.in)
		if err != nil {
			return "", err
		}
		// Windows pipes end lines with CRLF; the agent should get plain LF.
		text := strings.ReplaceAll(string(b), "\r\n", "\n")
		return strings.TrimRight(text, "\n"), nil
	}
	return strings.Join(words, " "), nil
}

func (a *app) uploadAll(ctx context.Context, files []string) ([]string, error) {
	var ids []string
	for _, f := range files {
		id, err := a.client.upload(ctx, f)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// waitFlags are shared by the commands that can wait for the agent's turn.
type waitFlags struct {
	wait    *bool
	timeout *time.Duration
}

func addWaitFlags(fs *flag.FlagSet) waitFlags {
	return waitFlags{
		wait:    fs.Bool("wait", false, "wait until the agent's turn ends, then print what it said"),
		timeout: fs.Duration("timeout", 110*time.Second, "how long --wait waits"),
	}
}

// afterAct either tells the reader how to follow up or, with --wait, waits
// for the turn and prints the new messages.
func (a *app) afterAct(ctx context.Context, short, id string, before time.Time, wf waitFlags) error {
	if !*wf.wait {
		if !a.json {
			fmt.Fprintf(a.out, "follow up: hc wait %s   then: hc read %s\n", short, short)
		}
		return nil
	}
	s, done, err := a.waitSettled(ctx, id, before, *wf.timeout)
	if err != nil {
		return err
	}
	if !done {
		if a.json {
			return a.printJSON(map[string]any{"session": s, "timedOut": true})
		}
		fmt.Fprintf(a.out, "still %s after %s; continue with: hc wait %s\n", s.Status, *wf.timeout, short)
		return nil
	}
	return a.cmdRead([]string{short})
}

func (a *app) cmdSend(args []string) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	var files stringList
	fs.Var(&files, "file", "attach a local file (repeatable)")
	wf := addWaitFlags(fs)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 {
		return usagef("send needs a session ID and text")
	}
	text, err := a.textArg(pos[1:])
	if err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" && len(files) == 0 {
		return usagef("send needs text (or \"-\" to read stdin) or --file")
	}
	ctx := context.Background()
	s, err := a.resolve(ctx, pos[0])
	if err != nil {
		return err
	}
	if a.isSelf(s) {
		return fmt.Errorf("%s is this session itself; refusing to send to yourself", pos[0])
	}
	uploads, err := a.uploadAll(ctx, files)
	if err != nil {
		return err
	}
	body := map[string]any{"text": text}
	if len(uploads) > 0 {
		body["uploads"] = uploads
	}
	if err := a.client.do(ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(s.ID)+"/messages", body, nil); err != nil {
		return err
	}
	if a.json && !*wf.wait {
		return a.printJSON(map[string]any{"ok": true, "session": s.ID})
	}
	if !a.json {
		note := ""
		if s.Status == model.StatusRunning {
			note = " (agent is busy; it is queued until the current turn ends)"
		}
		fmt.Fprintf(a.out, "sent to %s%s\n", pos[0], note)
	}
	return a.afterAct(ctx, pos[0], s.ID, s.UpdatedAt, wf)
}

func (a *app) cmdAnswer(args []string) error {
	fs := flag.NewFlagSet("answer", flag.ContinueOnError)
	iaID := fs.String("ia", "", "which pending interaction (id prefix), when there are several")
	wf := addWaitFlags(fs)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		return usagef("answer needs a session ID and an answer (see \"hc show ID\")")
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
	ia, err := pickInteraction(pendingInteractions(msgs), *iaID, s)
	if err != nil {
		return err
	}
	if !ia.Supported {
		return fmt.Errorf("this %s cannot be answered through the Gateway; use the terminal: hc term %s, then hc keys %s KEY...", ia.Type, pos[0], pos[0])
	}
	res, summary, err := buildResponse(ia, pos[1:])
	if err != nil {
		return err
	}
	if err := a.client.do(ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(s.ID)+"/respond", res, nil); err != nil {
		return err
	}
	if a.json && !*wf.wait {
		return a.printJSON(map[string]any{"ok": true, "response": res})
	}
	if !a.json {
		fmt.Fprintf(a.out, "answered %s: %s\n", pos[0], summary)
	}
	return a.afterAct(ctx, pos[0], s.ID, s.UpdatedAt, wf)
}

func pickInteraction(pending []model.Interaction, prefix string, s model.Session) (model.Interaction, error) {
	var found []model.Interaction
	for _, ia := range pending {
		if prefix == "" || strings.HasPrefix(ia.ID, prefix) {
			found = append(found, ia)
		}
	}
	switch {
	case len(found) == 1:
		return found[0], nil
	case len(found) == 0 && prefix != "":
		return model.Interaction{}, fmt.Errorf("no pending interaction %q", prefix)
	case len(found) == 0:
		return model.Interaction{}, fmt.Errorf("nothing is waiting for an answer (status %s); to send a new prompt use hc send", s.Status)
	}
	var ids []string
	for _, ia := range found {
		ids = append(ids, ia.ID)
	}
	return model.Interaction{}, usagef("%d interactions are pending; pick one with --ia: %s", len(found), strings.Join(ids, ", "))
}

var decisionAliases = map[string]string{
	"approve": model.DecisionApprove, "yes": model.DecisionApprove, "y": model.DecisionApprove, "ok": model.DecisionApprove,
	"approve_session": model.DecisionApproveSession, "always": model.DecisionApproveSession,
	"deny": model.DecisionDeny, "no": model.DecisionDeny, "n": model.DecisionDeny, "reject": model.DecisionDeny,
}

// buildResponse turns "approve", "2", "q1=2 q2=Other text" or plain free
// text into the Gateway's answer, checking it against the question first so
// a mistake is reported here instead of being typed into the agent.
func buildResponse(ia model.Interaction, args []string) (model.InteractionResponse, string, error) {
	res := model.InteractionResponse{InteractionID: ia.ID}
	if len(ia.Questions) == 0 {
		decisions := ia.Decisions
		if len(decisions) == 0 {
			decisions = []string{model.DecisionApprove, model.DecisionDeny}
		}
		d := decisionAliases[strings.ToLower(strings.Join(args, " "))]
		if d == "" || !slices.Contains(decisions, d) {
			return res, "", usagef("answer with one of: %s", strings.Join(decisions, " | "))
		}
		res.Decision = d
		return res, d, nil
	}
	values := map[string]string{}
	var loose []string
	for _, arg := range args {
		if k, v, ok := strings.Cut(arg, "="); ok && questionByID(ia.Questions, k) != nil {
			values[k] = v
			continue
		}
		loose = append(loose, arg)
	}
	if len(loose) > 0 {
		if len(ia.Questions) != 1 || len(values) > 0 {
			var ids []string
			for _, q := range ia.Questions {
				ids = append(ids, q.ID+"=…")
			}
			return res, "", usagef("%d questions: answer each as %s", len(ia.Questions), strings.Join(ids, " "))
		}
		values[ia.Questions[0].ID] = strings.Join(loose, " ")
	}
	res.Answers = map[string]model.Answer{}
	var summary []string
	for _, q := range ia.Questions {
		v, ok := values[q.ID]
		if !ok {
			return res, "", usagef("question %s is not answered (answer as %s=…)", q.ID, q.ID)
		}
		ans, err := answerQuestion(q, v)
		if err != nil {
			return res, "", err
		}
		res.Answers[q.ID] = ans
		s := strings.Join(ans.Selected, ", ")
		if ans.Text != "" {
			s = strings.TrimPrefix(s+", ", ", ") + strconv.Quote(ans.Text)
		}
		summary = append(summary, q.ID+"="+s)
	}
	return res, strings.Join(summary, " "), nil
}

func questionByID(qs []model.Question, id string) *model.Question {
	for i := range qs {
		if qs[i].ID == id {
			return &qs[i]
		}
	}
	return nil
}

func answerQuestion(q model.Question, v string) (model.Answer, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return model.Answer{}, usagef("empty answer for %s", q.ID)
	}
	if q.Type == model.QuestionText || len(q.Options) == 0 {
		return model.Answer{Text: v}, nil
	}
	parts := []string{v}
	if q.Type == model.QuestionMultiSelect {
		parts = strings.Split(v, ",")
	}
	var labels []string
	for _, p := range parts {
		label, ok := optionLabel(q.Options, strings.TrimSpace(p))
		if !ok {
			labels = nil
			break
		}
		labels = append(labels, label)
	}
	if labels != nil {
		return model.Answer{Selected: labels}, nil
	}
	if q.AllowOther {
		return model.Answer{Text: v}, nil
	}
	var opts []string
	for i, op := range q.Options {
		opts = append(opts, fmt.Sprintf("%d) %s", i+1, op.Label))
	}
	return model.Answer{}, usagef("%q is not an option of %s and free text is not allowed; options: %s", v, q.ID, strings.Join(opts, "  "))
}

// optionLabel accepts an option's number (1-based) or its label.
func optionLabel(opts []model.Option, v string) (string, bool) {
	if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= len(opts) {
		return opts[n-1].Label, true
	}
	for _, op := range opts {
		if strings.EqualFold(op.Label, v) {
			return op.Label, true
		}
	}
	return "", false
}

type startResult struct {
	SessionID     string `json:"sessionId,omitempty"`
	PaneID        string `json:"paneId"`
	Warning       string `json:"warning,omitempty"`
	TrustRequired bool   `json:"trustRequired,omitempty"`
}

func (a *app) cmdStart(args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	provider := fs.String("provider", "claude", "claude, codex, opencode or devin")
	cwd := fs.String("cwd", "", "working directory (default: current directory)")
	modelID := fs.String("model", "", "model id (see the app or GET /v1/models)")
	effort := fs.String("effort", "", "reasoning effort id")
	mode := fs.String("mode", "", "start mode id, e.g. plan")
	worktree := fs.Bool("worktree", false, "start in a new git worktree of cwd")
	noTrust := fs.Bool("no-trust", false, "do not accept the folder-trust dialog")
	var files stringList
	fs.Var(&files, "file", "attach a local file to the first prompt (repeatable)")
	wf := addWaitFlags(fs)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	prompt, err := a.textArg(pos)
	if err != nil {
		return err
	}
	dir := *cwd
	if dir == "" {
		if !a.client.conn.local {
			return usagef("--cwd is required when the Gateway runs on another machine: give a directory on that machine")
		}
		if dir, err = os.Getwd(); err != nil {
			return err
		}
	}
	ctx := context.Background()
	uploads, err := a.uploadAll(ctx, files)
	if err != nil {
		return err
	}
	body := map[string]any{
		"provider": *provider, "cwd": dir, "prompt": prompt, "model": *modelID, "effort": *effort,
		"mode": *mode, "trust": !*noTrust, "worktree": *worktree,
	}
	if len(uploads) > 0 {
		body["uploads"] = uploads
	}
	var res startResult
	if err := a.client.do(ctx, http.MethodPost, "/v1/sessions", body, &res); err != nil {
		return err
	}
	if res.SessionID == "" && *wf.wait {
		res.SessionID = a.awaitIdentity(ctx, res.PaneID, *wf.timeout)
	}
	if a.json && !*wf.wait {
		return a.printJSON(res)
	}
	short := nativeID(res.SessionID)
	if !a.json {
		if res.SessionID != "" {
			short = clipID(short, 8)
			fmt.Fprintf(a.out, "started %s (pane %s)\n", res.SessionID, res.PaneID)
		} else {
			fmt.Fprintf(a.out, "started in pane %s; the agent has not reported its session id yet (find it with hc ls)\n", res.PaneID)
		}
		if res.Warning != "" {
			fmt.Fprintf(a.out, "warning: %s\n", res.Warning)
		}
		if res.TrustRequired {
			fmt.Fprintf(a.out, "stopped at the folder-trust dialog; see: hc term %s\n", res.PaneID)
		}
	}
	if res.SessionID == "" {
		return nil
	}
	return a.afterAct(ctx, short, res.SessionID, time.Time{}, wf)
}

// awaitIdentity waits for the agent in pane to report its session id.
func (a *app) awaitIdentity(ctx context.Context, pane string, timeout time.Duration) string {
	deadline := time.Now().Add(min(timeout, time.Minute))
	for time.Now().Before(deadline) {
		if list, err := a.client.sessions(ctx, false); err == nil {
			for _, s := range list {
				if s.PaneID == pane && s.Status != model.StatusOffline {
					return s.ID
				}
			}
		}
		time.Sleep(pollInterval)
	}
	return ""
}

// terminalPath is the session's terminal, or a launch's while its agent
// has no session id yet.
func (a *app) terminalPath(ctx context.Context, ref string) (string, error) {
	s, err := a.resolve(ctx, ref)
	if err == nil {
		return "/v1/sessions/" + url.PathEscape(s.ID) + "/terminal", nil
	}
	var probe json.RawMessage
	launch := "/v1/launches/" + url.PathEscape(ref) + "/terminal"
	if a.client.do(ctx, http.MethodGet, launch+"?lines=1", nil, &probe) == nil {
		return launch, nil
	}
	return "", err
}

func (a *app) cmdTerm(args []string) error {
	fs := flag.NewFlagSet("term", flag.ContinueOnError)
	lines := fs.Int("lines", 60, "lines from the bottom of the screen (1-2000)")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("term takes one session ID (or the pane id of a starting session)")
	}
	ctx := context.Background()
	path, err := a.terminalPath(ctx, pos[0])
	if err != nil {
		return err
	}
	var res struct {
		PaneID   string `json:"paneId"`
		Text     string `json:"text"`
		Revision int64  `json:"revision"`
	}
	if err := a.client.do(ctx, http.MethodGet, path+"?lines="+strconv.Itoa(*lines), nil, &res); err != nil {
		return err
	}
	if a.json {
		return a.printJSON(res)
	}
	fmt.Fprintln(a.out, strings.TrimRight(res.Text, " \n"))
	return nil
}

var terminalKeys = []string{
	"enter", "esc", "tab", "shift+tab", "space", "backspace", "up", "down", "left", "right",
	"ctrl+c", "ctrl+d", "1", "2", "3", "4", "5", "6", "7", "8", "9", "y", "n",
}

func (a *app) cmdKeys(args []string) error {
	fs := flag.NewFlagSet("keys", flag.ContinueOnError)
	text := fs.String("text", "", "text to type before the keys")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 || (len(pos) == 1 && *text == "") {
		return usagef("keys needs a session ID and keys (%s) and/or --text", strings.Join(terminalKeys, " "))
	}
	keys := []string{}
	for _, k := range pos[1:] {
		k = strings.ToLower(k)
		if !slices.Contains(terminalKeys, k) {
			return usagef("unknown key %q; keys: %s", k, strings.Join(terminalKeys, " "))
		}
		keys = append(keys, k)
	}
	ctx := context.Background()
	path, err := a.terminalPath(ctx, pos[0])
	if err != nil {
		return err
	}
	if err := a.client.do(ctx, http.MethodPost, path, map[string]any{"text": *text, "keys": keys}, nil); err != nil {
		return err
	}
	if a.json {
		return a.printJSON(map[string]bool{"ok": true})
	}
	fmt.Fprintf(a.out, "sent; check the screen with: hc term %s\n", pos[0])
	return nil
}

func (a *app) cmdMode(args []string) error {
	pos, err := parseFlags(flag.NewFlagSet("mode", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("mode takes one session ID")
	}
	ctx := context.Background()
	s, err := a.resolve(ctx, pos[0])
	if err != nil {
		return err
	}
	if err := a.client.do(ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(s.ID)+"/mode", map[string]any{}, nil); err != nil {
		return err
	}
	time.Sleep(700 * time.Millisecond)
	after, err := a.client.session(ctx, s.ID)
	if err != nil {
		return err
	}
	if a.json {
		return a.printJSON(map[string]string{"from": s.Mode, "mode": after.Mode})
	}
	fmt.Fprintf(a.out, "mode: %s → %s (run again to cycle further)\n", firstNonEmpty(s.Mode, "default"), firstNonEmpty(after.Mode, "default"))
	return nil
}

func (a *app) cmdArchive(args []string) error {
	pos, err := parseFlags(flag.NewFlagSet("archive", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return usagef("archive takes one or more session IDs")
	}
	ctx := context.Background()
	var ids []string
	for _, ref := range pos {
		s, err := a.resolve(ctx, ref)
		if err != nil {
			return err
		}
		if a.isSelf(s) {
			return fmt.Errorf("%s is this session itself; refusing to archive yourself", ref)
		}
		ids = append(ids, s.ID)
	}
	var res struct {
		Sessions []struct {
			model.Session
			Warning string `json:"warning,omitempty"`
		} `json:"sessions"`
	}
	if err := a.client.do(ctx, http.MethodPost, "/v1/sessions/archive", map[string]any{"ids": ids}, &res); err != nil {
		return err
	}
	if a.json {
		return a.printJSON(res)
	}
	for _, s := range res.Sessions {
		line := "archived " + s.ID
		if s.Warning != "" {
			line += " (warning: " + s.Warning + ")"
		}
		fmt.Fprintln(a.out, line)
	}
	return nil
}

func (a *app) cmdResume(args []string) error {
	pos, err := parseFlags(flag.NewFlagSet("resume", flag.ContinueOnError), args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("resume takes one session ID")
	}
	ctx := context.Background()
	s, err := a.resolve(ctx, pos[0])
	if err != nil {
		return err
	}
	var res startResult
	if err := a.client.do(ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(s.ID)+"/resume", map[string]bool{"trust": true}, &res); err != nil {
		return err
	}
	if a.json {
		return a.printJSON(res)
	}
	fmt.Fprintf(a.out, "resumed %s in pane %s\n", s.ID, res.PaneID)
	if res.Warning != "" {
		fmt.Fprintf(a.out, "warning: %s\n", res.Warning)
	}
	return nil
}

func (a *app) cmdUsage(args []string) error {
	if _, err := parseFlags(flag.NewFlagSet("usage", flag.ContinueOnError), args); err != nil {
		return err
	}
	var u model.Usage
	if err := a.client.do(context.Background(), http.MethodGet, "/v1/usage", nil, &u); err != nil {
		return err
	}
	if a.json {
		return a.printJSON(u)
	}
	if len(u.Providers) == 0 {
		fmt.Fprintln(a.out, firstNonEmpty(u.Error, "no usage data"))
		return nil
	}
	for _, p := range u.Providers {
		var parts []string
		for _, w := range p.Windows {
			s := fmt.Sprintf("%s %d%%", strings.TrimSpace(w.Label+" "+w.Scope), w.UsedPercent)
			if w.ResetsAt != nil {
				s += " (resets " + clock(*w.ResetsAt) + ")"
			}
			parts = append(parts, s)
		}
		line := strings.TrimSpace(p.DisplayName+" "+p.Plan) + ": " + strings.Join(parts, " · ")
		if p.Error != "" {
			line += " [error: " + p.Error + "]"
		}
		fmt.Fprintln(a.out, line)
	}
	if u.Error != "" {
		fmt.Fprintln(a.out, "last refresh failed:", u.Error)
	}
	return nil
}
