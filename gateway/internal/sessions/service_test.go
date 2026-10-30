package sessions

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/runtime"
)

func Testランタイムの状態を共通ステータスに正規化する(t *testing.T) {
	live := func(s string) *providers.Live { return &providers.Live{State: s} }
	cases := []struct {
		name string
		live *providers.Live
		sum  providers.Summary
		want model.Status
	}{
		{"どこでも動いていない", nil, providers.Summary{}, model.StatusOffline},
		{"履歴が作業中でも動いていない", nil, providers.Summary{Status: model.StatusRunning}, model.StatusOffline},
		{"履歴が承認待ちでも動いていない", nil, providers.Summary{Status: model.StatusWaitingApproval}, model.StatusOffline},
		{"作業中", live(providers.StateWorking), providers.Summary{}, model.StatusRunning},
		{"質問待ち", live(providers.StateBlocked), providers.Summary{Pending: model.InteractionQuestions}, model.StatusWaitingInput},
		{"承認待ち", live(providers.StateBlocked), providers.Summary{Pending: model.InteractionApproval}, model.StatusWaitingApproval},
		{"種別不明のブロック", live(providers.StateBlocked), providers.Summary{}, model.StatusWaitingInput},
		{"完了", live(providers.StateDone), providers.Summary{}, model.StatusCompleted},
		{"エラーで完了", live(providers.StateDone), providers.Summary{LastTurnFailed: true}, model.StatusFailed},
		{"待機中", live(providers.StateIdle), providers.Summary{}, model.StatusIdle},
		{"判定不能", live("unknown"), providers.Summary{}, model.StatusIdle},
		{"provider優先", live(providers.StateIdle), providers.Summary{Status: model.StatusWaitingApproval}, model.StatusWaitingApproval},
		{"ターン中にプロセスが落ちた", &providers.Live{State: providers.StateIdle, Failed: true}, providers.Summary{}, model.StatusFailed},
	}
	for _, c := range cases {
		if got := Normalize(c.live, &c.sum); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

type stubProvider struct{ providers.Provider }

func (stubProvider) Name() string        { return "claude" }
func (stubProvider) DisplayName() string { return "Claude Code" }
func (stubProvider) Summary(_ context.Context, id string, _ *providers.Live) (*providers.Summary, error) {
	return &providers.Summary{NativeID: id, Cwd: "/w"}, nil
}

// withLive returns a service over p whose runtime runs the given native ids.
func withLive(t *testing.T, window time.Duration, p providers.Provider, ids ...string) *Service {
	t.Helper()
	rt := &directStub{Provider: p, infos: map[string]providers.DirectInfo{}}
	m, err := runtime.New(filepath.Join(t.TempDir(), "d.json"), time.Hour, rt)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := m.Resume(context.Background(), "claude:"+id, providers.DirectStart{Cwd: "/w"}); err != nil {
			t.Fatal(err)
		}
	}
	s := New(window, p)
	s.Direct = m
	return s
}

func Test稼働中セッションのcwdはファイルリンクと同じランタイムのcwdを使う(t *testing.T) {
	r := &Resolved{Provider: stubProvider{}, NativeID: "x", Live: &providers.Live{Cwd: "/work/app"}}
	if got := toSession(r, &providers.Summary{Cwd: "/work/app/sub"}).Cwd; got != "/work/app" {
		t.Errorf("live cwd = %q", got)
	}
	if got := toSession(r, &providers.Summary{Cwd: "/work/app/sub", PinnedCwd: true}).Cwd; got != "/work/app/sub" {
		t.Errorf("pinned cwd = %q", got)
	}
	off := &Resolved{Provider: stubProvider{}, NativeID: "x"}
	if got := toSession(off, &providers.Summary{Cwd: "/work/app/sub"}).Cwd; got != "/work/app/sub" {
		t.Errorf("offline cwd = %q", got)
	}
}

func Test終了ターンの内容の版をセッションへ引き継ぐ(t *testing.T) {
	r := &Resolved{Provider: stubProvider{}, NativeID: "x"}
	if got := toSession(r, &providers.Summary{CompletionRevision: "turn-1"}).CompletionRevision; got != "turn-1" {
		t.Fatalf("completion revision = %q", got)
	}
}

type conversationProvider struct {
	stubProvider
	err error
}

func (p conversationProvider) Conversation(context.Context, string, *providers.Live) (*providers.Summary, []model.Message, error) {
	return nil, nil, p.err
}

func Test一括取得でも履歴未保存の稼働中セッションを開ける(t *testing.T) {
	for _, tc := range []struct {
		name    string
		id      string
		err     error
		wantErr error
	}{
		{"稼働中の履歴未保存", "claude:live", providers.ErrNotFound, nil},
		{"停止中の履歴なし", "claude:offline", providers.ErrNotFound, providers.ErrNotFound},
		{"稼働中の取得失敗", "claude:live", context.DeadlineExceeded, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := withLive(t, 0, conversationProvider{err: tc.err}, "live")
			sess, msgs, err := s.Conversation(context.Background(), tc.id)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && (sess.ID != tc.id || !sess.CanSend || sess.Cwd != "/w" || len(msgs) != 0) {
				t.Fatalf("session = %+v, messages = %+v", sess, msgs)
			}
		})
	}
}

type listArchive struct{}

func (listArchive) Has(id string) bool { return id == "claude:archived" }
func (listArchive) IDs() []string      { return []string{"claude:archived", "codex:other"} }

type filteredListProvider struct {
	stubProvider
	excluded map[string]bool
}

func (p *filteredListProvider) Summary(context.Context, string, *providers.Live) (*providers.Summary, error) {
	return &providers.Summary{NativeID: "live"}, nil
}
func (p *filteredListProvider) RecentExcluding(_ context.Context, _ time.Time, exclude map[string]bool) ([]providers.Summary, error) {
	p.excluded = exclude
	return []providers.Summary{{NativeID: "offline"}}, nil
}
func Test一覧は稼働中とアーカイブ済みを解析前の除外対象にする(t *testing.T) {
	p := &filteredListProvider{}
	s := withLive(t, time.Hour, p, "live")
	s.Archive = listArchive{}
	got, err := s.List(context.Background())
	if err != nil || len(got) != 2 {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if len(p.excluded) != 2 || !p.excluded["live"] || !p.excluded["archived"] {
		t.Fatalf("excluded=%v", p.excluded)
	}
}

// blockingProvider blocks every Summary and Recent call until release is
// closed, reporting each call on started.
type blockingProvider struct {
	stubProvider
	started chan string
	release chan struct{}
}

func (p *blockingProvider) Summary(_ context.Context, id string, _ *providers.Live) (*providers.Summary, error) {
	p.started <- "summary:" + id
	<-p.release
	return &providers.Summary{NativeID: id}, nil
}

func (p *blockingProvider) Recent(context.Context, time.Time) ([]providers.Summary, error) {
	p.started <- "recent"
	<-p.release
	return []providers.Summary{{NativeID: "offline"}}, nil
}

func Test一覧は稼働中セッションの要約と最近のセッション取得を並行して行う(t *testing.T) {
	p := &blockingProvider{started: make(chan string, 3), release: make(chan struct{})}
	s := withLive(t, time.Hour, p, "a", "b")
	type result struct {
		list []model.Session
		err  error
	}
	done := make(chan result, 1)
	go func() {
		list, err := s.List(context.Background())
		done <- result{list, err}
	}()
	for range 3 {
		select {
		case <-p.started:
		case <-time.After(2 * time.Second):
			close(p.release)
			t.Fatal("summaries and recent sessions were not read concurrently")
		}
	}
	close(p.release)
	r := <-done
	if r.err != nil || len(r.list) != 3 {
		t.Fatalf("list=%v err=%v", r.list, r.err)
	}
}

func Test止まった直接管理のセッションも一覧に出て送信できる(t *testing.T) {
	s := withLive(t, time.Hour, stubProvider{}, "s1")
	ctx := context.Background()
	if err := s.Direct.Stop(ctx, "claude:s1"); err != nil {
		t.Fatal(err)
	}
	r, err := s.Resolve(ctx, "claude:s1")
	if err != nil || r.Live == nil || !r.Live.Dormant {
		t.Fatalf("resolved = %+v, %v", r, err)
	}
	list, err := s.LiveSessions(ctx)
	if err != nil || len(list) != 1 || !list[0].CanSend || list[0].Status != model.StatusIdle {
		t.Fatalf("live sessions = %+v, %v", list, err)
	}
}

// directStub adds a direct runtime to a provider.
type directStub struct {
	providers.Provider
	infos map[string]providers.DirectInfo
}

func (d *directStub) DirectStart(_ context.Context, o providers.DirectStart) (string, error) {
	id := o.Resume
	if id == "" {
		id = "s1"
	}
	d.infos[id] = providers.DirectInfo{NativeID: id, Cwd: o.Cwd, Alive: true}
	return id, nil
}
func (d *directStub) DirectStop(_ context.Context, id string) error {
	in := d.infos[id]
	in.Alive = false
	d.infos[id] = in
	return nil
}
func (d *directStub) DirectInterrupt(context.Context, string) error         { return nil }
func (d *directStub) DirectSetModel(context.Context, string, string) error  { return nil }
func (d *directStub) DirectSetEffort(context.Context, string, string) error { return nil }
func (d *directStub) DirectSetMode(context.Context, string, string) error   { return nil }
func (d *directStub) SettingsApply() string                                 { return providers.ApplyNextTurn }
func (d *directStub) SetDirectListener(func(string))                        {}
func (d *directStub) DirectSessions() []providers.DirectInfo {
	var out []providers.DirectInfo
	for _, in := range d.infos {
		out = append(out, in)
	}
	return out
}
