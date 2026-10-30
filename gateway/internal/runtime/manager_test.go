package runtime

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// fakeRuntime is a DirectRuntime whose sessions the test drives.
type fakeRuntime struct {
	mu       sync.Mutex
	infos    map[string]*providers.DirectInfo
	starts   []providers.DirectStart
	models   []string
	listener func(string)
}

func (f *fakeRuntime) Name() string        { return "claude" }
func (f *fakeRuntime) DisplayName() string { return "Claude Code" }
func (f *fakeRuntime) HerdrAgent() string  { return "claude" }
func (f *fakeRuntime) Summary(context.Context, string, *providers.Live) (*providers.Summary, error) {
	return nil, providers.ErrNotFound
}
func (f *fakeRuntime) Recent(context.Context, time.Time) ([]providers.Summary, error) {
	return nil, nil
}
func (f *fakeRuntime) Messages(context.Context, string, *providers.Live) ([]model.Message, error) {
	return nil, nil
}
func (f *fakeRuntime) Image(context.Context, string, string, int) (string, []byte, error) {
	return "", nil, providers.ErrNotFound
}
func (f *fakeRuntime) Send(context.Context, string, *providers.Live, model.Input) error { return nil }
func (f *fakeRuntime) Respond(context.Context, string, *providers.Live, model.InteractionResponse) error {
	return nil
}

func (f *fakeRuntime) DirectStart(_ context.Context, opts providers.DirectStart) (string, error) {
	f.mu.Lock()
	f.starts = append(f.starts, opts)
	id := opts.Resume
	if id == "" {
		id = "s1"
	}
	if f.infos == nil {
		f.infos = map[string]*providers.DirectInfo{}
	}
	f.infos[id] = &providers.DirectInfo{NativeID: id, Cwd: opts.Cwd, Alive: true, Model: opts.Model}
	f.mu.Unlock()
	f.changed(id)
	return id, nil
}

func (f *fakeRuntime) DirectStop(_ context.Context, id string) error {
	f.set(id, func(in *providers.DirectInfo) { in.Alive = false })
	return nil
}
func (f *fakeRuntime) DirectInterrupt(context.Context, string) error { return nil }
func (f *fakeRuntime) DirectSetModel(_ context.Context, id, m string) error {
	f.mu.Lock()
	f.models = append(f.models, m)
	f.mu.Unlock()
	return nil
}
func (f *fakeRuntime) DirectSetEffort(context.Context, string, string) error { return nil }
func (f *fakeRuntime) DirectSetMode(context.Context, string, string) error   { return nil }
func (f *fakeRuntime) SettingsApply() string                                 { return providers.ApplyNextRequest }
func (f *fakeRuntime) SetDirectListener(l func(string))                      { f.listener = l }

func (f *fakeRuntime) DirectSessions() []providers.DirectInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []providers.DirectInfo
	for _, in := range f.infos {
		out = append(out, *in)
	}
	return out
}

func (f *fakeRuntime) set(id string, fn func(*providers.DirectInfo)) {
	f.mu.Lock()
	if in := f.infos[id]; in != nil {
		fn(in)
	}
	f.mu.Unlock()
	f.changed(id)
}

func (f *fakeRuntime) changed(id string) {
	if f.listener != nil {
		f.listener(id)
	}
}

func newManager(t *testing.T, path string) (*Manager, *fakeRuntime) {
	t.Helper()
	rt := &fakeRuntime{}
	m, err := New(path, 72*time.Hour, rt)
	if err != nil {
		t.Fatal(err)
	}
	return m, rt
}

func Testターンが終わると未読の完了になり既読で待機に戻る(t *testing.T) {
	m, rt := newManager(t, filepath.Join(t.TempDir(), "direct.json"))
	ctx := context.Background()
	id, err := m.Start(ctx, "claude", providers.DirectStart{Cwd: "/w"})
	if err != nil || id != "claude:s1" {
		t.Fatalf("start = %q, %v", id, err)
	}
	rt.set("s1", func(in *providers.DirectInfo) { in.Running = true })
	if s := m.Sessions()[id]; s.Live.HerdrStatus != herdr.StatusWorking || s.Live.Dormant {
		t.Fatalf("running = %+v", s.Live)
	}
	before := m.Sessions()[id].Live.Revision
	rt.set("s1", func(in *providers.DirectInfo) { in.Running, in.Turns = false, 1 })
	s := m.Sessions()[id]
	if s.Live.HerdrStatus != herdr.StatusDone || s.Live.Revision == before {
		t.Fatalf("finished = %+v", s.Live)
	}
	m.MarkSeen(id)
	if s := m.Sessions()[id]; s.Live.HerdrStatus != herdr.StatusIdle {
		t.Errorf("seen = %+v", s.Live)
	}
}

func Test実行中に停止したGatewayを再起動するとそのセッションは失敗として残る(t *testing.T) {
	path := filepath.Join(t.TempDir(), "direct.json")
	m, rt := newManager(t, path)
	ctx := context.Background()
	id, _ := m.Start(ctx, "claude", providers.DirectStart{Cwd: "/w", Model: "haiku"})
	rt.set("s1", func(in *providers.DirectInfo) { in.Running = true })
	m.Shutdown()
	rt.set("s1", func(in *providers.DirectInfo) { in.Alive, in.Running = false, false })

	m2, rt2 := newManager(t, path)
	s, ok := m2.Sessions()[id]
	if !ok || !s.Live.Dormant || !s.Live.Failed || s.Error == "" {
		t.Fatalf("after restart = %+v ok=%v", s, ok)
	}
	// 次の送信の前に同じIDのまま新しいプロセスで再開し、何も再送しない
	if err := m2.EnsureRunning(ctx, id); err != nil {
		t.Fatal(err)
	}
	if len(rt2.starts) != 1 || rt2.starts[0].Resume != "s1" || rt2.starts[0].Model != "haiku" || !rt2.starts[0].Prompt.Empty() {
		t.Errorf("resume = %+v", rt2.starts)
	}
	if s := m2.Sessions()[id]; s.Live.Dormant || s.Live.Failed {
		t.Errorf("resumed = %+v", s.Live)
	}
}

func Testプロセスがターン中に終了すると失敗になる(t *testing.T) {
	m, rt := newManager(t, filepath.Join(t.TempDir(), "direct.json"))
	id, _ := m.Start(context.Background(), "claude", providers.DirectStart{Cwd: "/w"})
	rt.set("s1", func(in *providers.DirectInfo) { in.Running = true })
	rt.set("s1", func(in *providers.DirectInfo) { in.Alive, in.Running, in.Error = false, false, "boom" })
	s := m.Sessions()[id]
	if !s.Live.Dormant || !s.Live.Failed || s.Error != "boom" {
		t.Errorf("after crash = %+v", s)
	}
}

func Test休眠中のモデル変更は再開するプロセスに引き継ぐ(t *testing.T) {
	m, rt := newManager(t, filepath.Join(t.TempDir(), "direct.json"))
	ctx := context.Background()
	id, _ := m.Start(ctx, "claude", providers.DirectStart{Cwd: "/w", Model: "haiku"})
	m.Stop(ctx, id)
	if err := m.SetModel(ctx, id, "sonnet"); err != nil {
		t.Fatal(err)
	}
	if len(rt.models) != 0 {
		t.Errorf("model sent to a stopped process: %v", rt.models)
	}
	m.EnsureRunning(ctx, id)
	if last := rt.starts[len(rt.starts)-1]; last.Model != "sonnet" || last.Cwd != "/w" {
		t.Errorf("resume = %+v", last)
	}
}

func Testアーカイブした記録は消え再起動後も戻らない(t *testing.T) {
	path := filepath.Join(t.TempDir(), "direct.json")
	m, _ := newManager(t, path)
	id, _ := m.Start(context.Background(), "claude", providers.DirectStart{Cwd: "/w"})
	if err := m.Forget(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	m2, _ := newManager(t, path)
	if _, ok := m2.Sessions()[id]; ok || m2.Owns(id) {
		t.Error("forgotten session came back")
	}
}

func Test長く使われていない休眠セッションの記録は捨てる(t *testing.T) {
	path := filepath.Join(t.TempDir(), "direct.json")
	m, _ := newManager(t, path)
	m.Start(context.Background(), "claude", providers.DirectStart{Cwd: "/w"})
	m.Stop(context.Background(), "claude:s1")
	m.now = func() time.Time { return time.Now().Add(100 * time.Hour) }
	if _, ok := m.Sessions()["claude:s1"]; ok {
		t.Error("stale record kept")
	}
}

func Test対応していないプロバイダーは直接管理を拒む(t *testing.T) {
	m, _ := newManager(t, filepath.Join(t.TempDir(), "direct.json"))
	if m.Supports("devin") {
		t.Error("devin supported")
	}
	if _, err := m.Start(context.Background(), "devin", providers.DirectStart{}); err == nil {
		t.Error("start succeeded")
	}
}
