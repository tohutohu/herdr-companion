package resident

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// fakeClock is a settable clock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}

func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// fakeEnv accepts every folder under root.
type fakeEnv struct {
	root    string
	trusted bool
	catalog providers.ModelCatalog
}

func (e *fakeEnv) ResolveDir(p string) (string, error) {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(e.root, real)
	if err != nil || rel == ".." || filepath.IsAbs(rel) || len(rel) >= 2 && rel[:2] == ".." {
		return "", errors.New("forbidden path")
	}
	return real, nil
}

func (e *fakeEnv) FolderTrusted(provider, cwd string) bool { return e.trusted }

func (e *fakeEnv) Catalog(ctx context.Context, provider string) (providers.ModelCatalog, error) {
	return e.catalog, nil
}

// fakeRunner answers runs from a script. A step can block until released.
type fakeRunner struct {
	mu       sync.Mutex
	calls    []RunRequest
	script   []fakeStep
	fallback fakeStep
	started  chan string
}

type fakeStep struct {
	result     any // marshalled into Structured; string is used as Text
	err        error
	denials    []string
	block      chan struct{}
	noSend     bool // fail before BeforeSend
	agentError string
}

func (f *fakeRunner) push(steps ...fakeStep) {
	f.mu.Lock()
	f.script = append(f.script, steps...)
	f.mu.Unlock()
}

func (f *fakeRunner) Run(ctx context.Context, req RunRequest, hooks RunHooks) (*RunOutput, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	step := f.fallback
	if len(f.script) > 0 {
		step, f.script = f.script[0], f.script[1:]
	}
	f.mu.Unlock()
	if step.noSend {
		return nil, step.err
	}
	if err := hooks.BeforeSend("native-"+req.RunID, "fake-model"); err != nil {
		return nil, err
	}
	hooks.Sent()
	if f.started != nil {
		f.started <- req.RunID
	}
	if step.block != nil {
		select {
		case <-step.block:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
	if step.err != nil {
		return nil, step.err
	}
	out := &RunOutput{NativeID: "native-" + req.RunID, Model: "fake-model", PermissionDenials: step.denials, Error: step.agentError,
		Usage: Usage{InputTokens: 10, OutputTokens: 5}}
	switch v := step.result.(type) {
	case string:
		out.Text = v
	case nil:
	default:
		b, _ := json.Marshal(v)
		out.Structured = b
	}
	return out, nil
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeRunner) lastPrompt() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return ""
	}
	return f.calls[len(f.calls)-1].Prompt
}

// result builds a contract result.
func result(outcome, summary string, opts ...func(map[string]any)) map[string]any {
	r := map[string]any{
		"schemaVersion": 1, "outcome": outcome, "summary": summary, "report": "",
		"notification": map[string]any{"requested": outcome == OutcomeChanged, "key": nil},
		"statePatch":   []any{}, "memorySummary": nil, "nextWakeAt": nil, "question": nil, "artifacts": []any{},
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

func withKey(k string) func(map[string]any) {
	return func(r map[string]any) { r["notification"] = map[string]any{"requested": true, "key": k} }
}

func withSet(key, valueJSON string) func(map[string]any) {
	return func(r map[string]any) {
		r["statePatch"] = append(r["statePatch"].([]any), map[string]any{"op": "set", "key": key, "value": valueJSON})
	}
}

func withWake(at time.Time) func(map[string]any) {
	return func(r map[string]any) { r["nextWakeAt"] = at.Format(time.RFC3339) }
}

func withQuestion(text string) func(map[string]any) {
	return func(r map[string]any) {
		r["question"] = map[string]any{"text": text, "options": []string{"はい", "いいえ"}}
	}
}

// fakeSender records pushes.
type fakeSender struct {
	mu   sync.Mutex
	sent []map[string]string
	fail map[string]error
}

func (s *fakeSender) Send(ctx context.Context, token string, data map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail[token]; err != nil {
		return err
	}
	cp := map[string]string{"token": token}
	for k, v := range data {
		cp[k] = v
	}
	s.sent = append(s.sent, cp)
	return nil
}

func (s *fakeSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

type harness struct {
	t      *testing.T
	dir    string
	db     string
	clock  *fakeClock
	env    *fakeEnv
	runner *fakeRunner
	sender *fakeSender
	cfg    *config.Store
	svc    *Service
	store  *Store
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	dir, _ = filepath.EvalSymlinks(dir)
	ws := filepath.Join(dir, "ws")
	os.MkdirAll(filepath.Join(ws, "repo"), 0o755)
	cfg, err := config.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.UpsertDevice("phone", "token-a")
	h := &harness{t: t, dir: dir, db: filepath.Join(dir, "state", "resident.db"),
		clock:  &fakeClock{now: mustTime(t, "2026-10-30T06:00:00+09:00")},
		env:    &fakeEnv{root: ws, trusted: true},
		runner: &fakeRunner{fallback: fakeStep{result: result(OutcomeUnchanged, "変化なし")}},
		sender: &fakeSender{fail: map[string]error{}}, cfg: cfg}
	h.open()
	return h
}

func (h *harness) open() {
	h.t.Helper()
	st, err := Open(h.db)
	if err != nil {
		h.t.Fatal(err)
	}
	h.store = st
	h.svc = New(st, h.env, map[string]Runner{ProviderClaude: h.runner, ProviderCodex: h.runner}, Config{}, h.clock.Now)
	h.svc.Notifier = &Notifier{Store: st, Sender: h.sender, Config: h.cfg, Now: h.clock.Now}
	h.t.Cleanup(func() { h.svc.Shutdown(5 * time.Second); st.Close() })
}

// restart simulates a gateway restart on the same database.
func (h *harness) restart() {
	h.t.Helper()
	h.svc.Shutdown(5 * time.Second)
	h.store.Close()
	h.open()
	if err := h.svc.Recover(context.Background()); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) cwd() string { return filepath.Join(h.env.root, "repo") }

func (h *harness) create(mod func(*CreateRequest)) *Task {
	h.t.Helper()
	req := CreateRequest{Settings: Settings{Name: "CI監視", Instruction: "CIを確認して", Provider: ProviderClaude, Cwd: h.cwd(),
		Schedule: Schedule{Kind: ScheduleDaily, Time: "07:00"}, Notify: Notify{OnFailure: true, OnQuestion: true}}}
	if mod != nil {
		mod(&req)
	}
	task, err := h.svc.CreateTask(context.Background(), req)
	if err != nil {
		h.t.Fatal(err)
	}
	return task
}

// settle ticks until no run is queued or running. Steps must not block.
func (h *harness) settle() {
	h.t.Helper()
	for i := 0; i < 100; i++ {
		h.svc.Tick(context.Background())
		h.svc.wg.Wait()
		var n int
		h.store.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE status IN ('queued','running')`).Scan(&n)
		if n == 0 {
			h.svc.Tick(context.Background())
			return
		}
	}
	h.t.Fatal("runs did not settle")
}

func (h *harness) runs(taskID string) []Run {
	h.t.Helper()
	rs, _, err := h.svc.Runs(context.Background(), taskID, "", 100)
	if err != nil {
		h.t.Fatal(err)
	}
	return rs
}

func (h *harness) messages(taskID string) []Message {
	h.t.Helper()
	ms, _, err := h.svc.Messages(context.Background(), taskID, "", 100)
	if err != nil {
		h.t.Fatal(err)
	}
	return ms
}

func (h *harness) notifications(taskID string) []Notification {
	h.t.Helper()
	ns, err := h.svc.Notifications(context.Background(), taskID, 100)
	if err != nil {
		h.t.Fatal(err)
	}
	return ns
}

func (h *harness) memory(taskID string) *Memory {
	h.t.Helper()
	m, err := h.svc.GetMemory(context.Background(), taskID)
	if err != nil {
		h.t.Fatal(err)
	}
	return m
}

func statuses(rs []Run) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Status)
	}
	return out
}

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}
