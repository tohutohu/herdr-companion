package resident

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRunPrecheck(t *testing.T) {
	run := func(t *testing.T, command string, timeout int, mem *Memory) (*PrecheckResult, error) {
		t.Helper()
		return RunPrecheck(context.Background(), PrecheckRequest{TaskID: "task_1", RunID: "run_1", Cwd: t.TempDir(),
			Triggers: []string{TriggerSchedule}, Precheck: Precheck{Command: command, TimeoutSeconds: timeout}, Memory: mem})
	}

	t.Run("exit 0なら一致として標準出力を返し、Memoryを標準入力・タスク情報を環境変数で渡す", func(t *testing.T) {
		mem := &Memory{Summary: "前回", State: map[string]json.RawMessage{"seen": json.RawMessage(`["a"]`)}}
		res, err := run(t, `cat; echo; echo "$HC_RESIDENT_TASK $HC_RESIDENT_RUN $HC_RESIDENT_TRIGGERS"; pwd`, 10, mem)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(res.Output), "\n")
		if !res.Matched() || len(lines) != 3 {
			t.Fatalf("result = %+v", res)
		}
		if lines[0] != `{"state":{"seen":["a"]},"summary":"前回"}` {
			t.Errorf("stdin = %s", lines[0])
		}
		if lines[1] != "task_1 run_1 schedule" {
			t.Errorf("env = %s", lines[1])
		}
	})

	t.Run("exit 1は不一致で、エラーにしない", func(t *testing.T) {
		res, err := run(t, "exit 1", 10, nil)
		if err != nil || res.Matched() || res.ExitCode != 1 {
			t.Fatalf("result = %+v, %v", res, err)
		}
	})

	t.Run("exit 0と1以外は標準エラーの末尾付きで失敗にする", func(t *testing.T) {
		_, err := run(t, "echo 取得できません >&2; exit 3", 10, nil)
		if !errors.Is(err, errPrecheck) || !strings.Contains(err.Error(), "exit 3: 取得できません") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("時間内に終わらなければ子プロセスごと止めて失敗にする", func(t *testing.T) {
		start := time.Now()
		_, err := run(t, "sleep 30 & sleep 30", 1, nil)
		if !errors.Is(err, errPrecheck) || !strings.Contains(err.Error(), "within 1 seconds") {
			t.Fatalf("err = %v", err)
		}
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("took %v", d)
		}
	})

	t.Run("標準出力は上限で切って読み続ける", func(t *testing.T) {
		res, err := run(t, "head -c 100000 /dev/zero | tr '\\0' x", 10, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Output) != maxPrecheckOutputBytes || !res.Truncated {
			t.Errorf("output %d bytes, truncated %v", len(res.Output), res.Truncated)
		}
	})
}

func TestPrecheckRuns(t *testing.T) {
	withPrecheck := func(command string) func(*CreateRequest) {
		return func(r *CreateRequest) { r.Precheck = &Precheck{Command: command} }
	}

	t.Run("定期実行で条件に合わなければエージェントを起動せずskippedとして残し、日次上限に数えない", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(func(r *CreateRequest) {
			withPrecheck("echo 新着なし; exit 1")(r)
			r.Limits.MaxRunsPerDay = 1
		})
		h.clock.Set(mustTime(t, "2026-10-30T07:00:10+09:00"))
		h.settle()
		if h.runner.callCount() != 0 {
			t.Fatal("the agent ran although the precheck found nothing")
		}
		rs := h.runs(task.ID)
		if len(rs) != 1 || rs[0].Status != RunSkipped || rs[0].Report != "新着なし" || !strings.Contains(rs[0].Summary, "事前チェック") {
			t.Fatalf("runs = %+v", rs)
		}
		if len(h.messages(task.ID)) != 0 || len(h.notifications(task.ID)) != 0 {
			t.Error("a skipped run added a message or a notification")
		}
		got, _ := h.svc.GetTask(bg, task.ID)
		if got.RunsToday != 0 || got.LastRun == nil || got.LastRun.Status != RunSkipped || got.State != TaskIdle {
			t.Errorf("task = %+v", got)
		}
		// The skip did not use up the day's only automatic start.
		if _, err := h.svc.UpdateTask(bg, task.ID, UpdateRequest{ExpectedVersion: got.Version,
			Schedule: &Schedule{Kind: ScheduleCron, Cron: "*/30 * * * *"}, Precheck: &Precheck{Command: "echo 新着あり"}}); err != nil {
			t.Fatal(err)
		}
		h.clock.Set(mustTime(t, "2026-10-30T07:30:10+09:00"))
		h.settle()
		if h.runner.callCount() != 1 {
			t.Fatalf("agent calls = %d, runs = %v", h.runner.callCount(), statuses(h.runs(task.ID)))
		}
	})

	t.Run("条件に合えばエージェントを起動し、コマンドの出力を外部データとしてプロンプトに入れる", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(withPrecheck("echo '新着: 物件A 8万円'"))
		h.clock.Set(mustTime(t, "2026-10-30T07:00:10+09:00"))
		h.settle()
		if h.runner.callCount() != 1 {
			t.Fatalf("agent calls = %d", h.runner.callCount())
		}
		p := h.runner.lastPrompt()
		if !strings.Contains(p, "found something to look at (exit 0)") || !strings.Contains(p, "新着: 物件A 8万円\n</precheck>") {
			t.Errorf("prompt = %s", p)
		}
		if i, j := strings.Index(p, "## 7. External data"), strings.Index(p, "<precheck "); i < 0 || j < i {
			t.Error("the precheck output is not in the external data section")
		}
		if rs := h.runs(task.ID); rs[0].Status != RunCompleted {
			t.Errorf("run = %+v", rs[0])
		}
	})

	t.Run("コマンドが失敗したらエージェントを起動せず失敗として通知する", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(withPrecheck("echo サイトに接続できません >&2; exit 2"))
		h.clock.Set(mustTime(t, "2026-10-30T07:00:10+09:00"))
		h.settle()
		if h.runner.callCount() != 0 {
			t.Fatal("the agent ran after a failed precheck")
		}
		rs := h.runs(task.ID)
		if len(rs) != 1 || rs[0].Status != RunFailed || rs[0].ErrorKind != ErrKindPrecheck || !strings.Contains(rs[0].Error, "サイトに接続できません") {
			t.Fatalf("runs = %+v", rs)
		}
		ns := h.notifications(task.ID)
		if len(ns) != 1 || ns[0].Kind != NotifyFailure {
			t.Fatalf("notifications = %+v", ns)
		}
		if got, _ := h.svc.GetTask(bg, task.ID); got.State != TaskNeedsAttention {
			t.Errorf("state = %s", got.State)
		}
	})

	t.Run("失敗のあとに条件なしで終われば要確認が解ける", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(withPrecheck("exit 2"))
		h.clock.Set(mustTime(t, "2026-10-30T07:00:10+09:00"))
		h.settle()
		got, _ := h.svc.GetTask(bg, task.ID)
		if _, err := h.svc.UpdateTask(bg, task.ID, UpdateRequest{ExpectedVersion: got.Version, Precheck: &Precheck{Command: "exit 1"}}); err != nil {
			t.Fatal(err)
		}
		h.clock.Set(mustTime(t, "2026-10-31T07:00:10+09:00"))
		h.settle()
		if got, _ := h.svc.GetTask(bg, task.ID); got.State != TaskIdle {
			t.Errorf("state = %s (%s)", got.State, got.Attention)
		}
	})

	t.Run("手動実行は条件に合わなくてもエージェントを起動し、その旨を伝える", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(withPrecheck("echo 0件; exit 1"))
		h.svc.RunNow(bg, task.ID, "")
		h.settle()
		if h.runner.callCount() != 1 {
			t.Fatalf("agent calls = %d", h.runner.callCount())
		}
		p := h.runner.lastPrompt()
		if !strings.Contains(p, "found nothing to look at (exit 1), but the user started this run") || !strings.Contains(p, "0件") {
			t.Errorf("prompt = %s", p)
		}
	})

	t.Run("空のコマンドで事前チェックを外せる", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(withPrecheck("exit 1"))
		got, err := h.svc.UpdateTask(bg, task.ID, UpdateRequest{ExpectedVersion: task.Version, Precheck: &Precheck{Command: "  "}})
		if err != nil || got.Precheck != nil {
			t.Fatalf("task = %+v, %v", got, err)
		}
		h.svc.RunNow(bg, task.ID, "")
		h.settle()
		if p := h.runner.lastPrompt(); strings.Contains(p, "Precheck:") {
			t.Error("a removed precheck still ran")
		}
	})

	t.Run("時間制限は既定60秒で、範囲外やRunの上限以上は拒否する", func(t *testing.T) {
		got, err := normalize(Settings{Instruction: "x", Provider: ProviderClaude, Cwd: "/tmp",
			Schedule: Schedule{Kind: ScheduleManual}, Precheck: &Precheck{Command: "true"}})
		if err != nil || got.Precheck.TimeoutSeconds != DefaultPrecheckTimeoutSeconds {
			t.Fatalf("precheck = %+v, %v", got.Precheck, err)
		}
		for _, p := range []Settings{
			{Precheck: &Precheck{Command: "true", TimeoutSeconds: 601}, Limits: Limits{MaxRunMinutes: 30}},
			{Precheck: &Precheck{Command: "true", TimeoutSeconds: 120}, Limits: Limits{MaxRunMinutes: 2}},
			{Precheck: &Precheck{Command: strings.Repeat("x", 5000)}},
		} {
			p.Instruction, p.Provider, p.Cwd, p.Schedule = "x", ProviderClaude, "/tmp", Schedule{Kind: ScheduleManual}
			if _, err := normalize(p); !errors.Is(err, ErrInvalid) {
				t.Errorf("%+v accepted: %v", p.Precheck, err)
			}
		}
	})
}
