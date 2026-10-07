// Package claude adapts Claude Code sessions. History comes from the session
// transcript JSONL; input and dialogs go through the Herdr pane (PTY).
package claude

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

const providerName = "claude"

// Max offline sessions returned by Recent.
const maxRecent = 30

type Provider struct {
	summaryMu    sync.Mutex
	summaries    map[summaryKey]cachedSummary
	transcriptMu sync.Mutex
	transcripts  map[string]*cachedTranscript
	modeMu       sync.Mutex
	modeOverride map[string]claudeModeOverride
	configDir    string
	term         providers.Terminal
	sink         deadletter.Sink
	// keyDelay separates dialog steps so the TUI can re-render.
	keyDelay time.Duration
}

// DefaultConfigDir honours CLAUDE_CONFIG_DIR like Claude Code itself.
func DefaultConfigDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func New(configDir string, term providers.Terminal, sink deadletter.Sink) *Provider {
	if configDir == "" {
		configDir = DefaultConfigDir()
	}
	return &Provider{configDir: configDir, term: term, sink: sink, keyDelay: 250 * time.Millisecond}
}

func (p *Provider) Name() string        { return providerName }
func (p *Provider) DisplayName() string { return "Claude Code" }
func (p *Provider) HerdrAgent() string  { return "claude" }

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

func gatewayID(native string) string { return providerName + ":" + native }

// transcriptPath locates <configDir>/projects/*/<id>.jsonl.
func (p *Provider) transcriptPath(nativeID string) (string, error) {
	if !sessionIDPattern.MatchString(nativeID) {
		return "", providers.ErrNotFound
	}
	matches, _ := filepath.Glob(filepath.Join(p.configDir, "projects", "*", nativeID+".jsonl"))
	if len(matches) == 0 {
		return "", providers.ErrNotFound
	}
	newest, newestTime := "", time.Time{}
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil && st.ModTime().After(newestTime) {
			newest, newestTime = m, st.ModTime()
		}
	}
	return newest, nil
}

func (p *Provider) load(nativeID string) (*Transcript, error) {
	path, err := p.transcriptPath(nativeID)
	if err != nil {
		return nil, err
	}
	return p.loadPath(path, nativeID)
}

func (p *Provider) loadPath(path, nativeID string) (*Transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	t, err := Decode(f, gatewayID(nativeID), p.sink)
	if err != nil {
		p.sink.Record(providerName, gatewayID(nativeID), deadletter.ProviderError, "read transcript: "+err.Error(), nil)
	}
	if t.Updated.IsZero() {
		if st, err := f.Stat(); err == nil {
			t.Updated = st.ModTime()
		}
	}
	return t, nil
}

func root(t *Transcript, live *providers.Live) string {
	if live != nil && live.Cwd != "" {
		return live.Cwd
	}
	return t.Cwd
}

func (p *Provider) Summary(ctx context.Context, nativeID string, live *providers.Live) (*providers.Summary, error) {
	reported := live
	live, screenPlan := p.dialogState(ctx, nativeID, live)
	path, err := p.transcriptPath(nativeID)
	if err != nil {
		return nil, err
	}
	s, err := p.summaryPath(ctx, path, nativeID, live)
	if err != nil {
		return nil, err
	}
	s.NativeID = nativeID
	if s.Cwd == "" && live != nil {
		s.Cwd = live.Cwd
	}
	p.applyModeOverride(nativeID, path, &s)
	if screenPlan != nil {
		s.Pending = model.InteractionApproval
		s.Status = model.StatusWaitingApproval
	}
	if live.Blocked() && !reported.Blocked() {
		dialogStatus(&s)
	}
	return &s, nil
}

func (p *Provider) Recent(ctx context.Context, since time.Time) ([]providers.Summary, error) {
	return p.RecentExcluding(ctx, since, nil)
}

func (p *Provider) RecentExcluding(ctx context.Context, since time.Time, exclude map[string]bool) ([]providers.Summary, error) {
	matches, err := filepath.Glob(filepath.Join(p.configDir, "projects", "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	type cand struct {
		path string
		mod  time.Time
	}
	var cands []cand
	for _, m := range matches {
		st, err := os.Stat(m)
		if err != nil || st.ModTime().Before(since) {
			continue
		}
		cands = append(cands, cand{m, st.ModTime()})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mod.After(cands[j].mod) })
	if len(cands) > maxRecent {
		cands = cands[:maxRecent]
	}
	var out []providers.Summary
	for _, c := range cands {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		id := strings.TrimSuffix(filepath.Base(c.path), ".jsonl")
		if exclude[id] {
			continue
		}
		s, err := p.summaryPath(ctx, c.path, id, nil)
		if err != nil {
			continue
		}
		if s.LastMessage == "" {
			continue // empty or metadata-only sessions
		}
		p.applyModeOverride(id, c.path, &s)
		s.NativeID = id
		out = append(out, s)
	}
	return out, nil
}

// Messages returns messages that may be shared with other callers; they must
// not be modified.
func (p *Provider) Messages(ctx context.Context, nativeID string, live *providers.Live) ([]model.Message, error) {
	live, screenPlan := p.dialogState(ctx, nativeID, live)
	path, err := p.transcriptPath(nativeID)
	if err != nil {
		return nil, err
	}
	c, err := p.transcript(path, nativeID)
	if err != nil {
		return nil, err
	}
	msgs := c.messages(ParseOptions{SessionID: gatewayID(nativeID), Root: root(c.t, live), Live: live, Sink: p.sink})
	updated := c.info.ModTime()
	c.mu.Unlock()
	return withScreenPlan(msgs, screenPlan, updated), nil
}

// SessionFileRoots returns the directories the session worked in, so file
// links made from an earlier cwd (see fileRefs) can be opened. The session
// could already read them through its terminal.
func (p *Provider) SessionFileRoots(ctx context.Context, nativeID string) []string {
	path, err := p.transcriptPath(nativeID)
	if err != nil {
		return nil
	}
	c, err := p.transcript(path, nativeID)
	if err != nil {
		return nil
	}
	defer c.mu.Unlock()
	return slices.Clone(c.t.cwds)
}

func (p *Provider) Image(ctx context.Context, nativeID, messageID string, index int) (string, []byte, error) {
	t, err := p.load(nativeID)
	if err != nil {
		return "", nil, err
	}
	src, ok := t.imageAt(messageID, index)
	if !ok || src.Type != "base64" {
		return "", nil, providers.ErrNotFound
	}
	data, err := base64.StdEncoding.DecodeString(src.Data)
	if err != nil {
		return "", nil, err
	}
	return src.MediaType, data, nil
}

// Send types the prompt into the Claude Code TUI. Each image path is pasted
// on its own (bracketed paste), which Claude Code turns into an [Image #N]
// attachment, before the text is submitted. Ordinary text is typed without
// bracketed-paste markers because Claude Code wraps sufficiently long pastes
// in <pasted_content>, even when they are regular prompts; see typePrompt for
// the long lines that are pasted anyway. Other attachments are named by path
// in the prompt text for Claude Code to read with its own tools.
func (p *Provider) Send(ctx context.Context, nativeID string, live *providers.Live, in model.Input) error {
	if live == nil {
		return providers.ErrNotLive
	}
	text := providers.TextWithFiles(in)
	if text == "" && len(in.Images) == 0 {
		return fmt.Errorf("empty message")
	}
	if p.dialogLive(ctx, nativeID, live).Blocked() {
		// Keep all input out of an agent dialog. agent.prompt performs this
		// check too, but ordinary prompts are sent through the PTY directly.
		return &herdr.Error{Code: "agent_blocked", Message: "agent is waiting at a dialog"}
	}
	return p.enter(ctx, live.PaneID, text, in.Images)
}

// SendLaunchPrompt uses the same raw TUI input path as a follow-up prompt.
// The generic Herdr prompt endpoint can be rendered as bracketed paste by
// Claude Code, which changes how an otherwise ordinary first prompt behaves.
func (p *Provider) SendLaunchPrompt(ctx context.Context, paneID string, in model.Input) error {
	return p.enter(ctx, paneID, providers.TextWithFiles(in), in.Images)
}

// ReportsSessionAtStartup marks that the Herdr SessionStart hook reports a
// new session once Claude Code reads its input. Input sent before that is
// dropped (verified with Claude Code 2.1.291).
func (p *Provider) ReportsSessionAtStartup() {}

// enter pastes each image path, then types and submits the text.
func (p *Provider) enter(ctx context.Context, paneID, text string, images []string) error {
	for _, img := range images {
		if err := p.term.SendText(ctx, paneID, "\x1b[200~"+img+"\x1b[201~"); err != nil {
			return err
		}
		// Claude Code reads the file asynchronously after each paste.
		if err := p.wait(ctx); err != nil {
			return err
		}
	}
	if text == "" {
		return p.submit(ctx, paneID)
	}
	return p.typePrompt(ctx, paneID, text)
}

const (
	// maxTypedLine is the longest line typed key by key. Claude Code handles
	// one terminal read longer than 800 UTF-16 units as an unbracketed paste,
	// and a long line arrives in several such reads: all but the last are
	// lost from the composer (verified with Claude Code 2.1.280).
	maxTypedLine = 800
	// submitKey is Enter in the kitty keyboard protocol (CSI 13 u). A plain
	// CR that Claude Code reads together with the end of a long line becomes
	// part of that paste, so the prompt gets a newline instead of being sent.
	// The escape sequence always starts a key event of its own.
	submitKey = "\x1b[13u"
)

// typePrompt enters text without bracketed-paste markers and submits it.
// Claude Code accepts Shift+Enter as an in-composer newline, so multiline
// prompts can use the same input path without turning into pasted content.
// Text with a line Claude Code could not take as typed input is sent as one
// bracketed paste; Claude Code records it in <pasted_content>, which the
// transcript parser removes again. Terminal control characters are left to
// agent.prompt, which can transport them safely as a single bracketed paste
// instead of being interpreted as key presses by the TUI.
func (p *Provider) typePrompt(ctx context.Context, paneID, text string) error {
	if strings.IndexFunc(text, func(r rune) bool {
		return (r < ' ' && r != '\n') || r == '\x7f'
	}) >= 0 {
		return p.term.Prompt(ctx, paneID, text)
	}

	lines := strings.Split(text, "\n")
	if slices.ContainsFunc(lines, func(line string) bool {
		return len(utf16.Encode([]rune(line))) > maxTypedLine
	}) {
		if err := p.term.SendText(ctx, paneID, "\x1b[200~"+text+"\x1b[201~"); err != nil {
			return err
		}
		return p.submit(ctx, paneID)
	}
	for i, line := range lines {
		if line != "" {
			if err := p.term.SendText(ctx, paneID, line); err != nil {
				return err
			}
		}
		if i == len(lines)-1 {
			break
		}
		if err := p.term.SendKeys(ctx, paneID, "shift+enter"); err != nil {
			return err
		}
	}
	return p.submit(ctx, paneID)
}

// submit presses Enter in the composer.
func (p *Provider) submit(ctx context.Context, paneID string) error {
	return p.term.SendText(ctx, paneID, submitKey)
}

// CycleMode uses Claude Code's built-in next-mode shortcut. Claude Code only
// records permissionMode in the transcript when a later prompt is written, so
// read the status line now and keep that label until the transcript catches
// up. This avoids sending a dummy prompt just to refresh the display.
func (p *Provider) CycleMode(ctx context.Context, nativeID string, live *providers.Live) error {
	if live == nil {
		return providers.ErrNotLive
	}
	path, _ := p.transcriptPath(nativeID)
	var before os.FileInfo
	if path != "" {
		before, _ = os.Stat(path)
	}
	if err := p.term.SendKeys(ctx, live.PaneID, "shift+tab"); err != nil {
		return err
	}
	if err := p.wait(ctx); err != nil {
		return err
	}
	result, err := p.term.ReadPane(ctx, live.PaneID, 40)
	if err != nil || result == nil {
		// The key was already delivered. A temporary inability to read the
		// pane must not turn a successful mode change into an API failure.
		return nil
	}
	label := claudeModeFromPane(result.Text)
	if label == "" || before == nil {
		return nil
	}
	p.modeMu.Lock()
	if p.modeOverride == nil {
		p.modeOverride = make(map[string]claudeModeOverride)
	}
	p.modeOverride[nativeID] = claudeModeOverride{label: label, transcript: before}
	p.modeMu.Unlock()
	return nil
}

func (p *Provider) wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(p.keyDelay):
		return nil
	}
}

func (p *Provider) Respond(ctx context.Context, nativeID string, live *providers.Live, r model.InteractionResponse) error {
	if live == nil {
		return providers.ErrNotLive
	}
	msgs, err := p.Messages(ctx, nativeID, live)
	if err != nil {
		return err
	}
	ia := findInteraction(msgs, r.InteractionID)
	if ia == nil || ia.State != model.InteractionPending {
		return providers.ErrInteractionGone
	}
	focusedRow := 0
	if ia.Kind == model.KindPlan {
		// Validate again immediately before answering. A stale lifecycle or
		// card must never send approval keys into an ordinary composer.
		screen, err := providers.Screen(ctx, p.term, live.PaneID)
		if err != nil && !errors.Is(err, providers.ErrUnsupported) {
			return err
		}
		if err == nil {
			current := screenPlan(screen)
			if current == nil || (strings.HasPrefix(ia.ID, screenPlanPrefix) && ia.ID != current.ID) {
				return providers.ErrInteractionGone
			}
			ia.Questions[0].Options = current.Questions[0].Options
			focusedRow = planFocusedRow(screen)
		}
	}
	steps, err := dialogKeys(ia, r)
	if err != nil {
		return err
	}
	if focusedRow > 0 {
		steps = append([]step{keys(repeat("up", focusedRow)...)}, steps...)
	}
	for i, st := range steps {
		if i > 0 {
			if err := p.wait(ctx); err != nil {
				return err
			}
		}
		switch {
		case st.prompt != "":
			if err := p.waitDialogClosed(ctx, live.PaneID); err != nil {
				return err
			}
			err = p.typePrompt(ctx, live.PaneID, st.prompt)
		case st.text != "":
			err = p.term.SendText(ctx, live.PaneID, st.text)
		default:
			err = p.term.SendKeys(ctx, live.PaneID, st.keys...)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func findInteraction(msgs []model.Message, id string) *model.Interaction {
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == model.BlockInteraction && b.Interaction.ID == id {
				return b.Interaction
			}
		}
	}
	return nil
}

// dialogFooter is the key hint line of Claude Code's question dialog.
const dialogFooter = "Enter to select"

// waitDialogClosed waits until the question dialog has left the screen, so a
// prompt typed next reaches the composer instead of the dialog, where "n"
// would open a notes field. A pane that cannot be read is not waited for.
func (p *Provider) waitDialogClosed(ctx context.Context, paneID string) error {
	for range 8 {
		res, err := p.term.ReadPane(ctx, paneID, 40)
		if err != nil || res == nil || !strings.Contains(res.Text, dialogFooter) {
			return nil
		}
		if err := p.wait(ctx); err != nil {
			return err
		}
	}
	return nil
}

// step is a batch of keys, literal text, or a prompt typed and submitted in
// the composer.
type step struct {
	keys   []string
	text   string
	prompt string
}

func keys(k ...string) step { return step{keys: k} }

func repeat(k string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = k
	}
	return out
}

// dialogKeys translates an answer into key presses for Claude Code's dialogs.
// Verified against Claude Code 2.1.x:
//   - permission prompt: "1. Yes" is focused, Enter accepts, Esc cancels.
//   - AskUserQuestion: one tab per question. Single select: arrows + Enter
//     (advances). "Type something." row accepts typed text. Multi select:
//     Space toggles, a "Submit" row follows "Type something".
//     With more than one question a review tab asks "Submit answers" (focused).
func dialogKeys(ia *model.Interaction, r model.InteractionResponse) ([]step, error) {
	switch ia.Type {
	case model.InteractionApproval:
		switch r.Decision {
		case model.DecisionApprove:
			return []step{keys("enter")}, nil
		case model.DecisionDeny:
			return []step{keys("esc")}, nil
		}
		return nil, fmt.Errorf("unsupported decision %q", r.Decision)
	case model.InteractionQuestions:
		if ia.Kind == model.KindPlan {
			return planKeys(ia, r)
		}
	default:
		return nil, providers.ErrUnsupported
	}
	if typedPreviewAnswer(ia, r) {
		// The dialog has no row to type this answer into. Cancel it and send
		// every answer as the next prompt instead.
		prompt, err := answersPrompt(ia, r)
		if err != nil {
			return nil, err
		}
		return []step{keys("esc"), {prompt: prompt}}, nil
	}

	var steps []step
	for _, q := range ia.Questions {
		a, ok := r.Answers[q.ID]
		if !ok {
			return nil, fmt.Errorf("missing answer for question %s", q.ID)
		}
		n := len(q.Options)
		switch q.Type {
		case model.QuestionSelect, model.QuestionText:
			if len(a.Selected) > 0 {
				idx := optionIndex(q, a.Selected[0])
				if idx < 0 {
					return nil, fmt.Errorf("unknown option %q", a.Selected[0])
				}
				steps = append(steps, keys(append(repeat("down", idx), "enter")...))
			} else if strings.TrimSpace(a.Text) != "" {
				if n > 0 {
					steps = append(steps, keys(repeat("down", n)...))
				}
				steps = append(steps, step{text: singleLine(a.Text)}, keys("enter"))
			} else {
				return nil, fmt.Errorf("empty answer for question %s", q.ID)
			}
		case model.QuestionMultiSelect:
			selected := map[int]bool{}
			for _, s := range a.Selected {
				idx := optionIndex(q, s)
				if idx < 0 {
					return nil, fmt.Errorf("unknown option %q", s)
				}
				selected[idx] = true
			}
			var ks []string
			for i := 0; i < n; i++ {
				if selected[i] {
					ks = append(ks, "space")
				}
				ks = append(ks, "down")
			}
			steps = append(steps, keys(ks...))
			// Focus is now on "Type something".
			if t := strings.TrimSpace(a.Text); t != "" {
				steps = append(steps, step{text: singleLine(t)})
			}
			steps = append(steps, keys("down", "enter"))
		}
	}
	if len(ia.Questions) > 1 {
		steps = append(steps, keys("enter")) // review tab: "Submit answers"
	}
	return steps, nil
}

// previewLayout reports whether Claude Code shows a question beside its
// options' previews. That layout lists only the options: there is no
// "Type something." row, and moving down from the last option focuses
// "Chat about this", where typed text is ignored (verified with Claude Code
// 2.1.286). Multi-select questions keep the ordinary layout.
func previewLayout(q model.Question) bool {
	if q.Type != model.QuestionSelect {
		return false
	}
	for _, o := range q.Options {
		if o.Preview != "" {
			return true
		}
	}
	return false
}

// typedPreviewAnswer reports whether a question shown beside previews is
// answered with text rather than one of its options.
func typedPreviewAnswer(ia *model.Interaction, r model.InteractionResponse) bool {
	for _, q := range ia.Questions {
		a := r.Answers[q.ID]
		if previewLayout(q) && len(a.Selected) == 0 && strings.TrimSpace(a.Text) != "" {
			return true
		}
	}
	return false
}

// answersPrompt writes each question with its answer on a line of its own.
func answersPrompt(ia *model.Interaction, r model.InteractionResponse) (string, error) {
	var lines []string
	for _, q := range ia.Questions {
		a, ok := r.Answers[q.ID]
		if !ok {
			return "", fmt.Errorf("missing answer for question %s", q.ID)
		}
		var parts []string
		for _, s := range a.Selected {
			if optionIndex(q, s) < 0 {
				return "", fmt.Errorf("unknown option %q", s)
			}
			parts = append(parts, s)
		}
		if t := strings.TrimSpace(a.Text); t != "" {
			parts = append(parts, t)
		}
		if len(parts) == 0 {
			return "", fmt.Errorf("empty answer for question %s", q.ID)
		}
		lines = append(lines, q.Question+" → "+strings.Join(parts, ", "))
	}
	return strings.Join(lines, "\n"), nil
}

func optionIndex(q model.Question, label string) int {
	for i, o := range q.Options {
		if o.Label == label {
			return i
		}
	}
	return -1
}

func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func (p *Provider) LaunchArgs(opts providers.LaunchOptions) []string {
	var args []string
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.Effort != "" {
		args = append(args, "--effort", opts.Effort)
	}
	if opts.Mode != "" {
		args = append(args, "--permission-mode", opts.Mode)
	}
	return args
}

// ResumeArgs continues the transcript under the same session id.
func (p *Provider) ResumeArgs(nativeID, cwd string) []string {
	return []string{"--resume", nativeID}
}

// Claude Code has no model catalog API; these are its CLI aliases, which
// always point at the latest model of each family.
var models = []providers.ModelOption{
	{ID: "fable", Name: "Fable", Description: "Most capable"},
	{ID: "opus", Name: "Opus", Description: "Complex tasks"},
	{ID: "sonnet", Name: "Sonnet", Description: "Everyday tasks"},
	{ID: "haiku", Name: "Haiku", Description: "Fastest"},
}

// Claude Code takes --effort for every model; the levels are the same ones
// its /effort command offers.
var efforts = []providers.EffortOption{
	{ID: "low", Name: providers.EffortName("low"), Description: "Quick answers"},
	{ID: "medium", Name: providers.EffortName("medium"), Description: "Balances speed and thinking"},
	{ID: "high", Name: providers.EffortName("high"), Description: "More thorough work"},
	{ID: "xhigh", Name: providers.EffortName("xhigh"), Description: "Extra thinking for hard problems"},
	{ID: "max", Name: providers.EffortName("max"), Description: "Maximum thinking"},
}

// The permission modes Claude Code's --permission-mode accepts, named as its
// status line names them so a session shows the mode it was started in.
var modes = []providers.ModeOption{
	{ID: "manual", Name: modeLabel("manual"), Description: "Asks before each change", Default: true},
	{ID: "plan", Name: modeLabel("plan"), Description: "Researches and plans without editing"},
	{ID: "acceptEdits", Name: modeLabel("acceptEdits"), Description: "Applies file edits without asking"},
	{ID: "auto", Name: modeLabel("auto"), Description: "Works on its own, asking only when it matters"},
	{ID: "dontAsk", Name: modeLabel("dontAsk"), Description: "Stops asking for the rest of the session"},
	// Bypass permissions is left out: Claude Code opens it with a warning
	// screen the launcher does not know how to answer, so the start would
	// stall on an unknown dialog. It stays reachable in the TUI.
}

func (p *Provider) Models(ctx context.Context) (providers.ModelCatalog, error) {
	return providers.ModelCatalog{Models: models, Efforts: efforts, Modes: modes}, nil
}

// StartupKeys accepts Claude Code's workspace trust dialog. Two variants exist:
//   - "❯ No, exit" / "Yes, I trust this folder" (No is focused)
//   - "Do you trust the files in this folder?" "❯ 1. Yes, proceed" (Yes is focused)
func (p *Provider) StartupKeys(screen string) []string {
	switch {
	case strings.Contains(screen, "Yes, I trust this folder"):
		return []string{"down", "enter"}
	case strings.Contains(screen, "Do you trust the files in this folder") && strings.Contains(screen, "Yes, proceed"):
		return []string{"enter"}
	}
	return nil
}
