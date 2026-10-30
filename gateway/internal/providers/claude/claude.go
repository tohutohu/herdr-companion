// Package claude adapts Claude Code sessions. History comes from the session
// transcript JSONL; the gateway runs sessions through the Agent SDK sidecar
// (direct.go).
package claude

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
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
	cacheBytes   int64
	configDir    string
	sink         deadletter.Sink
	// direct runs sessions through the Agent SDK sidecar (direct.go).
	direct directState
}

// DefaultConfigDir honours CLAUDE_CONFIG_DIR like Claude Code itself.
func DefaultConfigDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func New(configDir string, sink deadletter.Sink) *Provider {
	if configDir == "" {
		configDir = DefaultConfigDir()
	}
	return &Provider{configDir: configDir, sink: sink}
}

func (p *Provider) Name() string        { return providerName }
func (p *Provider) DisplayName() string { return "Claude Code" }

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
	if live != nil {
		return p.directSummary(ctx, nativeID, live)
	}
	path, err := p.transcriptPath(nativeID)
	if err != nil {
		return nil, err
	}
	s, err := p.summaryPath(ctx, path, nativeID, nil)
	if err != nil {
		return nil, err
	}
	s.NativeID = nativeID
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
		s.NativeID = id
		out = append(out, s)
	}
	return out, nil
}

// Messages returns messages that may be shared with other callers; they must
// not be modified.
func (p *Provider) Messages(ctx context.Context, nativeID string, live *providers.Live) ([]model.Message, error) {
	if live != nil {
		return p.directMessages(nativeID, live)
	}
	path, err := p.transcriptPath(nativeID)
	if err != nil {
		return nil, err
	}
	c, err := p.transcript(path, nativeID)
	if err != nil {
		return nil, err
	}
	msgs := c.messages(ParseOptions{SessionID: gatewayID(nativeID), Root: c.t.Cwd, Sink: p.sink})
	c.mu.Unlock()
	return p.resolveSessionSenders(msgs), nil
}

// SessionFileRoots returns the directories the session worked in, so file
// links made from an earlier cwd (see fileRefs) can be opened. The session
// could already read them with its own tools.
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

// Send gives the message to the session's query in the sidecar.
func (p *Provider) Send(ctx context.Context, nativeID string, live *providers.Live, in model.Input) error {
	if live == nil {
		return providers.ErrNotLive
	}
	return p.directSend(ctx, nativeID, in)
}

// Respond answers a permission request or question the sidecar forwarded.
func (p *Provider) Respond(ctx context.Context, nativeID string, live *providers.Live, r model.InteractionResponse) error {
	if live == nil {
		return providers.ErrNotLive
	}
	return p.directRespond(ctx, nativeID, r)
}

// Claude Code has no model catalog API; these are its CLI aliases, which
// always point at the latest model of each family.
var models = []providers.ModelOption{
	{ID: "fable", Name: "Fable", Description: "Most capable"},
	{ID: "opus", Name: "Opus", Description: "Complex tasks"},
	{ID: "sonnet", Name: "Sonnet", Description: "Everyday tasks"},
	{ID: "haiku", Name: "Haiku", Description: "Fastest"},
}

// Claude Code offers an effort for every model; the levels are the same ones
// its /effort command offers.
var efforts = []providers.EffortOption{
	{ID: "low", Name: providers.EffortName("low"), Description: "Quick answers"},
	{ID: "medium", Name: providers.EffortName("medium"), Description: "Balances speed and thinking"},
	{ID: "high", Name: providers.EffortName("high"), Description: "More thorough work"},
	{ID: "xhigh", Name: providers.EffortName("xhigh"), Description: "Extra thinking for hard problems"},
	{ID: "max", Name: providers.EffortName("max"), Description: "Maximum thinking"},
}

// The permission modes Claude Code accepts, named as its status line names
// them so a session shows the mode it was started in.
var modes = []providers.ModeOption{
	{ID: "manual", Name: modeLabel("manual"), Description: "Asks before each change", Default: true},
	{ID: "plan", Name: modeLabel("plan"), Description: "Researches and plans without editing"},
	{ID: "acceptEdits", Name: modeLabel("acceptEdits"), Description: "Applies file edits without asking"},
	{ID: "auto", Name: modeLabel("auto"), Description: "Works on its own, asking only when it matters"},
	{ID: "dontAsk", Name: modeLabel("dontAsk"), Description: "Stops asking for the rest of the session"},
	// Bypass permissions is never set by the gateway.
}

func (p *Provider) Models(ctx context.Context) (providers.ModelCatalog, error) {
	return providers.ModelCatalog{Models: models, Efforts: efforts, Modes: modes}, nil
}

// directSummary summarizes a live session: the transcript, with the pending
// interaction kind from the runtime.
func (p *Provider) directSummary(ctx context.Context, nativeID string, live *providers.Live) (*providers.Summary, error) {
	path, err := p.transcriptPath(nativeID)
	if err != nil {
		return nil, err
	}
	s, err := p.summaryPath(ctx, path, nativeID, parseLive(live))
	if err != nil {
		return nil, err
	}
	s.NativeID = nativeID
	if s.Cwd == "" {
		s.Cwd = live.Cwd
	}
	pending, _, _, _ := p.directSnapshot(nativeID)
	s.Pending = pendingKind(pending)
	return &s, nil
}

// directMessages is the transcript plus what only the runtime knows yet:
// pending approvals and questions, and the reply being streamed.
func (p *Provider) directMessages(nativeID string, live *providers.Live) ([]model.Message, error) {
	pending, partialID, partialText, partialAt := p.directSnapshot(nativeID)
	path, err := p.transcriptPath(nativeID)
	if errors.Is(err, providers.ErrNotFound) {
		// A new session writes its transcript with the first prompt.
		return withDirectState(nil, pending, partialID, partialText, partialAt, false), nil
	}
	if err != nil {
		return nil, err
	}
	c, err := p.transcript(path, nativeID)
	if err != nil {
		return nil, err
	}
	pl := parseLive(live)
	msgs := c.messages(ParseOptions{SessionID: gatewayID(nativeID), Root: root(c.t, pl), Live: pl, Sink: p.sink})
	inTranscript := partialID != "" && c.t.counted[partialID]
	c.mu.Unlock()
	msgs = p.resolveSessionSenders(msgs)
	return withDirectState(msgs, pending, partialID, partialText, partialAt, inTranscript), nil
}
