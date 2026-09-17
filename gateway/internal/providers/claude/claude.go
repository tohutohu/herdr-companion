// Package claude adapts Claude Code sessions. History comes from the session
// transcript JSONL; input and dialogs go through the Herdr pane (PTY).
package claude

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

const providerName = "claude"

// Max offline sessions returned by Recent.
const maxRecent = 30

type Provider struct {
	configDir string
	term      providers.Terminal
	sink      deadletter.Sink
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
	t, err := p.load(nativeID)
	if err != nil {
		return nil, err
	}
	s := t.summary(ParseOptions{SessionID: gatewayID(nativeID), Live: live})
	s.NativeID = nativeID
	if s.Cwd == "" && live != nil {
		s.Cwd = live.Cwd
	}
	return &s, nil
}

func (p *Provider) Recent(ctx context.Context, since time.Time) ([]providers.Summary, error) {
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
		t, err := p.loadPath(c.path, id)
		if err != nil {
			continue
		}
		s := t.summary(ParseOptions{SessionID: gatewayID(id)})
		if s.LastMessage == "" {
			continue // empty or metadata-only sessions
		}
		s.NativeID = id
		out = append(out, s)
	}
	return out, nil
}

func (p *Provider) Messages(ctx context.Context, nativeID string, live *providers.Live) ([]model.Message, error) {
	t, err := p.load(nativeID)
	if err != nil {
		return nil, err
	}
	return t.Messages(ParseOptions{SessionID: gatewayID(nativeID), Root: root(t, live), Live: live, Sink: p.sink}), nil
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
// attachment, before the text is submitted.
func (p *Provider) Send(ctx context.Context, nativeID string, live *providers.Live, in model.Input) error {
	if live == nil {
		return providers.ErrNotLive
	}
	text := strings.TrimSpace(in.Text)
	if text == "" && len(in.Images) == 0 {
		return fmt.Errorf("empty message")
	}
	if len(in.Images) > 0 && live.Blocked() {
		// agent.prompt checks this too, but only after the pastes were typed.
		return &herdr.Error{Code: "agent_blocked", Message: "agent is waiting at a dialog"}
	}
	for _, img := range in.Images {
		if err := p.term.SendText(ctx, live.PaneID, "\x1b[200~"+img+"\x1b[201~"); err != nil {
			return err
		}
		// Claude Code reads the file asynchronously after each paste.
		if err := p.wait(ctx); err != nil {
			return err
		}
	}
	if text == "" {
		return p.term.SendKeys(ctx, live.PaneID, "enter")
	}
	return p.term.Prompt(ctx, live.PaneID, text)
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
	steps, err := dialogKeys(ia, r)
	if err != nil {
		return err
	}
	for i, st := range steps {
		if i > 0 {
			if err := p.wait(ctx); err != nil {
				return err
			}
		}
		if st.text != "" {
			err = p.term.SendText(ctx, live.PaneID, st.text)
		} else {
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

// step is either a batch of keys or literal text.
type step struct {
	keys []string
	text string
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
	default:
		return nil, providers.ErrUnsupported
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

func (p *Provider) LaunchArgs(modelID, cwd string) []string {
	if modelID == "" {
		return nil
	}
	return []string{"--model", modelID}
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

func (p *Provider) Models(ctx context.Context) ([]providers.ModelOption, error) {
	return models, nil
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
