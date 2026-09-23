package sessions

import (
	"context"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

func TestHerdrの状態を共通ステータスに正規化する(t *testing.T) {
	live := func(s string) *providers.Live { return &providers.Live{PaneID: "w1:p1", HerdrStatus: s} }
	cases := []struct {
		name string
		live *providers.Live
		sum  providers.Summary
		want model.Status
	}{
		{"Herdrにいない", nil, providers.Summary{}, model.StatusOffline},
		{"作業中", live(herdr.StatusWorking), providers.Summary{}, model.StatusRunning},
		{"質問待ち", live(herdr.StatusBlocked), providers.Summary{Pending: model.InteractionQuestions}, model.StatusWaitingInput},
		{"承認待ち", live(herdr.StatusBlocked), providers.Summary{Pending: model.InteractionApproval}, model.StatusWaitingApproval},
		{"種別不明のブロック", live(herdr.StatusBlocked), providers.Summary{}, model.StatusWaitingInput},
		{"完了", live(herdr.StatusDone), providers.Summary{}, model.StatusCompleted},
		{"エラーで完了", live(herdr.StatusDone), providers.Summary{LastTurnFailed: true}, model.StatusFailed},
		{"待機中", live(herdr.StatusIdle), providers.Summary{}, model.StatusIdle},
		{"判定不能", live(herdr.StatusUnknown), providers.Summary{}, model.StatusIdle},
		{"provider優先", live(herdr.StatusIdle), providers.Summary{Status: model.StatusWaitingApproval}, model.StatusWaitingApproval},
	}
	for _, c := range cases {
		if got := Normalize(c.live, &c.sum); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func Test別のエージェントに置き換わったペインのセッション参照は無視する(t *testing.T) {
	shell := "zsh"
	claude := "claude"
	snap := &herdr.Snapshot{Panes: []herdr.Pane{
		{PaneID: "w1:p1", Agent: &shell, AgentSession: &herdr.AgentSession{Agent: "claude", Kind: "id", Value: "old"}},
		{PaneID: "w1:p2", Agent: &claude, AgentSession: &herdr.AgentSession{Agent: "claude", Kind: "id", Value: "new"}},
	}}
	s := New(nil, 0, stubProvider{})
	live := s.live(snap)
	if _, ok := live["claude:old"]; ok {
		t.Error("stale session reference should be ignored")
	}
	if r, ok := live["claude:new"]; !ok || r.Live.PaneID != "w1:p2" {
		t.Errorf("live = %+v", live)
	}
}

type stubProvider struct{ providers.Provider }

func (stubProvider) Name() string       { return "claude" }
func (stubProvider) HerdrAgent() string { return "claude" }

func Test稼働中セッションのcwdはファイルリンクと同じペインのcwdを使う(t *testing.T) {
	r := &Resolved{Provider: stubProvider{}, NativeID: "x", Live: &providers.Live{PaneID: "w1:p1", Cwd: "/work/app"}}
	if got := toSession(r, &providers.Summary{Cwd: "/work/app/sub"}).Cwd; got != "/work/app" {
		t.Errorf("live cwd = %q", got)
	}
	off := &Resolved{Provider: stubProvider{}, NativeID: "x"}
	if got := toSession(off, &providers.Summary{Cwd: "/work/app/sub"}).Cwd; got != "/work/app/sub" {
		t.Errorf("offline cwd = %q", got)
	}
}

func (stubProvider) DisplayName() string { return "Claude Code" }

type listSnapshot struct{}

func (listSnapshot) Snapshot(context.Context) (*herdr.Snapshot, error) {
	agent := "claude"
	return &herdr.Snapshot{Panes: []herdr.Pane{{PaneID: "pane", Agent: &agent, AgentSession: &herdr.AgentSession{Agent: agent, Kind: "id", Value: "live"}}}}, nil
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
	s := New(listSnapshot{}, time.Hour, p)
	s.Archive = listArchive{}
	got, err := s.List(context.Background())
	if err != nil || len(got) != 2 {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if len(p.excluded) != 2 || !p.excluded["live"] || !p.excluded["archived"] {
		t.Fatalf("excluded=%v", p.excluded)
	}
}

type twoLiveSnapshot struct{}

func (twoLiveSnapshot) Snapshot(context.Context) (*herdr.Snapshot, error) {
	agent := "claude"
	return &herdr.Snapshot{Panes: []herdr.Pane{
		{PaneID: "w1:p1", Agent: &agent, AgentSession: &herdr.AgentSession{Agent: agent, Kind: "id", Value: "a"}},
		{PaneID: "w2:p1", Agent: &agent, AgentSession: &herdr.AgentSession{Agent: agent, Kind: "id", Value: "b"}},
	}}, nil
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
	s := New(twoLiveSnapshot{}, time.Hour, p)
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
