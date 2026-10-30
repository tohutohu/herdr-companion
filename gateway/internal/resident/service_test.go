package resident

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

var bg = context.Background()

func TestCreateAndScheduledRun(t *testing.T) {
	t.Run("作成すると次回予定ができ、時刻になると実行されて結果が確定する", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		if task.NextRunAt == nil || !task.NextRunAt.Equal(mustTime(t, "2026-10-30T07:00:00+09:00")) {
			t.Fatalf("nextRunAt = %v", task.NextRunAt)
		}
		h.settle()
		if h.runner.callCount() != 0 {
			t.Fatal("ran before the scheduled time")
		}
		h.runner.push(fakeStep{result: result(OutcomeChanged, "CIが復旧しました", withKey("ci:run456"), withSet("lastRun", `"run456"`))})
		h.clock.Set(mustTime(t, "2026-10-30T07:00:10+09:00"))
		h.settle()
		rs := h.runs(task.ID)
		if len(rs) != 1 || rs[0].Status != RunCompleted || rs[0].Outcome != OutcomeChanged || rs[0].Triggers[0] != TriggerSchedule {
			t.Fatalf("runs = %+v", rs)
		}
		if got := string(h.memory(task.ID).State["lastRun"]); got != `"run456"` {
			t.Errorf("memory lastRun = %s", got)
		}
		ms := h.messages(task.ID)
		if len(ms) != 1 || ms[0].Kind != MsgReport || ms[0].Text != "CIが復旧しました" {
			t.Errorf("messages = %+v", ms)
		}
		ns := h.notifications(task.ID)
		if len(ns) != 1 || ns[0].Status != NotifSent || ns[0].Kind != NotifyChange {
			t.Fatalf("notifications = %+v", ns)
		}
		push := h.sender.sent[0]
		if push["type"] != "resident" || push["taskId"] != task.ID || push["runId"] != rs[0].ID || push["notificationId"] != ns[0].ID || push["sessionId"] != "" {
			t.Errorf("push = %v", push)
		}
		got, _ := h.svc.GetTask(bg, task.ID)
		if got.NextRunAt == nil || !got.NextRunAt.Equal(mustTime(t, "2026-10-31T07:00:00+09:00")) {
			t.Errorf("next = %v", got.NextRunAt)
		}
		if got.LastSuccessAt == nil || got.LatestReport == nil || got.LatestReport.Summary != "CIが復旧しました" || got.Unread != 1 {
			t.Errorf("task = %+v", got)
		}
		if err := h.svc.MarkRead(bg, task.ID, ""); err != nil {
			t.Fatal(err)
		}
		if got, _ := h.svc.GetTask(bg, task.ID); got.Unread != 0 {
			t.Errorf("unread after read = %d", got.Unread)
		}
	})

	t.Run("定期実行のunchangedは会話にも通知にも出さず履歴だけに残す", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		h.clock.Set(mustTime(t, "2026-10-30T07:00:00+09:00"))
		h.settle()
		if rs := h.runs(task.ID); len(rs) != 1 || rs[0].Outcome != OutcomeUnchanged {
			t.Fatalf("runs = %+v", rs)
		}
		if len(h.messages(task.ID)) != 0 || len(h.notifications(task.ID)) != 0 {
			t.Error("an unchanged scheduled run added a message or a notification")
		}
		if got, _ := h.svc.GetTask(bg, task.ID); got.Unread != 0 {
			t.Errorf("unread = %d", got.Unread)
		}
	})

	t.Run("手動の質問への返答は変化がなくても会話に返す", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		h.runner.push(fakeStep{result: result(OutcomeUnchanged, "まだ変化はありません", func(r map[string]any) { r["report"] = "詳しく調べました: 変化なし" })})
		if _, err := h.svc.PostMessage(bg, task.ID, "詳しく調べて", "req-follow-1"); err != nil {
			t.Fatal(err)
		}
		h.settle()
		ms := h.messages(task.ID)
		if len(ms) != 2 || ms[0].Role != RoleUser || ms[1].Text != "詳しく調べました: 変化なし" {
			t.Fatalf("messages = %+v", ms)
		}
		if len(h.notifications(task.ID)) != 0 {
			t.Error("an unchanged reply was pushed")
		}
	})
}

func TestResultsThatAreNotSuccess(t *testing.T) {
	cases := []struct {
		name     string
		step     fakeStep
		wantKind string
	}{
		{"不正なJSONは結果契約エラーで失敗にする", fakeStep{result: "了解しました。変化はありません。"}, ErrKindContract},
		{"結果がないときは失敗にする", fakeStep{}, ErrKindContract},
		{"必須項目の欠けた結果は失敗にする", fakeStep{result: map[string]any{"schemaVersion": 1, "outcome": "unchanged", "summary": "x"}}, ErrKindContract},
		{"取得失敗の報告は失敗として扱う", fakeStep{result: result(OutcomeFailed, "GitHubに接続できません")}, ErrKindAgent},
		{"権限拒否があったunchangedは成功にしない", fakeStep{result: result(OutcomeUnchanged, "変化なし"), denials: []string{"Bash(gh run list)"}}, ErrKindPermission},
		{"エージェントのエラーで終わったターンは失敗にする", fakeStep{agentError: "API error"}, ErrKindProvider},
		{"実行基盤のエラーは失敗にする", fakeStep{err: errors.New("sidecar exited")}, ErrKindProvider},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			task := h.create(nil)
			h.runner.push(fakeStep{result: result(OutcomeChanged, "初回", withSet("seen", `["a"]`))})
			h.svc.RunNow(bg, task.ID, "")
			h.settle()
			mem := h.memory(task.ID)
			h.runner.push(c.step)
			h.svc.RunNow(bg, task.ID, "")
			h.settle()
			rs := h.runs(task.ID)
			if rs[0].Status != RunFailed || rs[0].ErrorKind != c.wantKind {
				t.Fatalf("run = %s/%s (%s), want failed/%s", rs[0].Status, rs[0].ErrorKind, rs[0].Error, c.wantKind)
			}
			if after := h.memory(task.ID); after.Version != mem.Version || string(after.State["seen"]) != `["a"]` {
				t.Errorf("memory changed after a failure: %+v", after)
			}
			ns := h.notifications(task.ID)
			if len(ns) == 0 || ns[0].Kind != NotifyFailure {
				t.Errorf("no failure notification: %+v", ns)
			}
			got, _ := h.svc.GetTask(bg, task.ID)
			if got.State != TaskNeedsAttention {
				t.Errorf("state = %s", got.State)
			}
		})
	}
	t.Run("続けて失敗しても通知は成功するまで1回だけ", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		h.runner.fallback = fakeStep{result: result(OutcomeFailed, "取得失敗")}
		for i := 0; i < 3; i++ {
			h.svc.RunNow(bg, task.ID, "")
			h.settle()
		}
		if n := len(h.notifications(task.ID)); n != 1 {
			t.Errorf("failure notifications = %d, want 1", n)
		}
		h.runner.fallback = fakeStep{result: result(OutcomeUnchanged, "復旧")}
		h.svc.RunNow(bg, task.ID, "")
		h.settle()
		h.runner.fallback = fakeStep{result: result(OutcomeFailed, "また失敗")}
		h.svc.RunNow(bg, task.ID, "")
		h.settle()
		if n := len(h.notifications(task.ID)); n != 2 {
			t.Errorf("failure notifications = %d, want 2 after a success in between", n)
		}
	})
}

func TestNotificationRules(t *testing.T) {
	t.Run("同じ通知キーは二度通知しない", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		for i := 0; i < 2; i++ {
			h.runner.push(fakeStep{result: result(OutcomeChanged, "CI復旧", withKey("ci:run456:success"))})
			h.svc.RunNow(bg, task.ID, "")
			h.settle()
		}
		if n := len(h.notifications(task.ID)); n != 1 {
			t.Errorf("notifications = %d", n)
		}
		if rs := h.runs(task.ID); rs[0].NotifyDecision != DecisionDuplicate {
			t.Errorf("decision = %s", rs[0].NotifyDecision)
		}
	})
	t.Run("通知を希望しないchangedは条件に従い通知しない", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		h.runner.push(fakeStep{result: result(OutcomeChanged, "小さな変化", func(r map[string]any) {
			r["notification"] = map[string]any{"requested": false, "key": nil}
		})})
		h.svc.RunNow(bg, task.ID, "")
		h.settle()
		if len(h.notifications(task.ID)) != 0 || h.runs(task.ID)[0].NotifyDecision != DecisionNotRequested {
			t.Error("notified without a request in changes mode")
		}
	})
	t.Run("クールダウン中の変化通知は抑制する", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(func(r *CreateRequest) { r.Notify.CooldownMinutes = 60 })
		h.runner.push(fakeStep{result: result(OutcomeChanged, "変化1", withKey("k1"))}, fakeStep{result: result(OutcomeChanged, "変化2", withKey("k2"))})
		h.svc.RunNow(bg, task.ID, "")
		h.settle()
		h.clock.Add(10 * time.Minute)
		h.svc.RunNow(bg, task.ID, "")
		h.settle()
		if n := len(h.notifications(task.ID)); n != 1 {
			t.Errorf("notifications = %d", n)
		}
	})
	t.Run("静穏時間中の通知は終わるまで保留する", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(func(r *CreateRequest) { r.Notify.QuietStart, r.Notify.QuietEnd = "23:00", "07:00" })
		h.runner.push(fakeStep{result: result(OutcomeChanged, "夜の変化", withKey("night"))})
		h.svc.RunNow(bg, task.ID, "")
		h.settle()
		ns := h.notifications(task.ID)
		if len(ns) != 1 || ns[0].Status != NotifPending || !ns[0].NotBefore.Equal(mustTime(t, "2026-10-30T07:00:00+09:00")) {
			t.Fatalf("notifications = %+v", ns)
		}
		h.clock.Set(mustTime(t, "2026-10-30T07:00:01+09:00"))
		h.svc.Tick(bg)
		if h.notifications(task.ID)[0].Status != NotifSent {
			t.Error("not sent after the quiet hours")
		}
	})
	t.Run("送信に失敗した端末だけに上限回数まで再送する", func(t *testing.T) {
		h := newHarness(t)
		h.cfg.UpsertDevice("tablet", "token-b")
		h.sender.fail["token-b"] = errors.New("unavailable")
		task := h.create(nil)
		h.runner.push(fakeStep{result: result(OutcomeChanged, "変化", withKey("x"))})
		h.svc.RunNow(bg, task.ID, "")
		h.settle()
		for i := 0; i < 20; i++ {
			h.clock.Add(7 * time.Hour)
			h.settle()
		}
		ns := h.notifications(task.ID)
		if ns[0].Status != NotifFailed || ns[0].Attempts != MaxNotificationAttempts {
			t.Errorf("notification = %+v", ns[0])
		}
		toA := 0
		for _, p := range h.sender.sent {
			if p["token"] == "token-a" {
				toA++
			}
		}
		if toA != 1 {
			t.Errorf("token-a got %d pushes, want 1: %v", toA, h.sender.sent)
		}
	})
	t.Run("通知送信前に再起動しても通知は失われず1回だけ送る", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		h.svc.Notifier.Sender = nil // the push could not go out before the stop
		h.svc.Notifier.Config = nil
		h.runner.push(fakeStep{result: result(OutcomeChanged, "変化", withKey("y"))})
		h.svc.RunNow(bg, task.ID, "")
		h.svc.Tick(bg)
		h.svc.wg.Wait()
		// The outbox entry exists and is pending until delivered.
		h.store.db.Exec(`UPDATE notifications SET status = 'pending', reason = ''`)
		h.restart()
		h.svc.Tick(bg)
		h.svc.Tick(bg)
		if h.sender.count() != 1 {
			t.Errorf("pushes = %d, want 1", h.sender.count())
		}
	})
}

func TestIdempotencyAndConcurrency(t *testing.T) {
	t.Run("同じrequestIdの再送では追加指示も手動実行も増えない", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		a, err := h.svc.PostMessage(bg, task.ID, "詳しく", "req-0001")
		if err != nil {
			t.Fatal(err)
		}
		b, err := h.svc.PostMessage(bg, task.ID, "詳しく", "req-0001")
		if err != nil || b.Message.ID != a.Message.ID || b.Run.ID != a.Run.ID {
			t.Fatalf("retry = %+v %v", b, err)
		}
		if _, err := h.svc.PostMessage(bg, task.ID, "別の内容", "req-0001"); !errors.Is(err, ErrConflict) {
			t.Errorf("reused id with another text: %v", err)
		}
		r1, _ := h.svc.RunNow(bg, task.ID, "run-0001")
		r2, _ := h.svc.RunNow(bg, task.ID, "run-0001")
		if r1.Run.ID != r2.Run.ID {
			t.Error("a retried run request made another run")
		}
		if n := len(h.messages(task.ID)); n != 1 {
			t.Errorf("messages = %d", n)
		}
		h.settle()
		if h.runner.callCount() != 1 {
			t.Errorf("runner calls = %d, want 1", h.runner.callCount())
		}
	})
	t.Run("並行リクエストでも同じタスクは同時に1Runで全体も既定1Run", func(t *testing.T) {
		h := newHarness(t)
		t1 := h.create(nil)
		t2 := h.create(func(r *CreateRequest) { r.Name = "別タスク" })
		block := make(chan struct{})
		h.runner.started = make(chan string, 10)
		h.runner.push(fakeStep{result: result(OutcomeUnchanged, "1"), block: block})
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				id := t1.ID
				if i%2 == 1 {
					id = t2.ID
				}
				h.svc.RunNow(bg, id, fmt.Sprintf("parallel-%04d", i))
				h.svc.Tick(bg)
			}()
		}
		wg.Wait()
		<-h.runner.started
		if n := len(h.svc.ActiveRuns()); n != 1 {
			t.Fatalf("active runs = %d, want 1", n)
		}
		var maxQueued int
		h.store.db.QueryRow(`SELECT COALESCE(MAX(n), 0) FROM (SELECT COUNT(*) n FROM runs WHERE status = 'queued' GROUP BY task_id)`).Scan(&maxQueued)
		if maxQueued > 1 {
			t.Errorf("a task has %d queued runs, want at most 1", maxQueued)
		}
		close(block)
		h.settle()
		if c := h.runner.callCount(); c > 3 {
			t.Errorf("runner calls = %d", c)
		}
	})
	t.Run("実行中に届いた追加指示は捨てずに次のRunで扱い、定期実行は1件にまとめる", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(func(r *CreateRequest) { r.Schedule = Schedule{Kind: ScheduleCron, Cron: "*/5 * * * *"} })
		block := make(chan struct{})
		h.runner.started = make(chan string, 10)
		h.runner.push(fakeStep{result: result(OutcomeUnchanged, "1"), block: block})
		h.svc.RunNow(bg, task.ID, "")
		h.svc.Tick(bg)
		<-h.runner.started
		h.svc.PostMessage(bg, task.ID, "PR #12も見て", "")
		for i := 0; i < 3; i++ { // three schedule occurrences while running
			h.clock.Add(5 * time.Minute)
			h.svc.Tick(bg)
		}
		var queued int
		h.store.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE task_id = ? AND status = 'queued'`, task.ID).Scan(&queued)
		if queued != 1 {
			t.Fatalf("queued runs = %d, want 1", queued)
		}
		close(block)
		h.runner.started = nil
		h.settle()
		if !strings.Contains(h.runner.lastPrompt(), "PR #12も見て") {
			t.Error("the follow-up was not in the next run's prompt")
		}
		rs := h.runs(task.ID)
		if !contains(rs[0].Triggers, TriggerFollowUp) || !contains(rs[0].Triggers, TriggerSchedule) || !rs[0].Interactive {
			t.Errorf("merged run = %+v", rs[0])
		}
	})
}

func TestScheduleChanges(t *testing.T) {
	t.Run("予定を変更すると旧Wakeupは起動しない", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		up, err := h.svc.UpdateTask(bg, task.ID, UpdateRequest{ExpectedVersion: task.Version, Schedule: &Schedule{Kind: ScheduleDaily, Time: "08:00"}})
		if err != nil {
			t.Fatal(err)
		}
		if !up.NextRunAt.Equal(mustTime(t, "2026-10-30T08:00:00+09:00")) {
			t.Errorf("next = %v", up.NextRunAt)
		}
		h.clock.Set(mustTime(t, "2026-10-30T07:30:00+09:00"))
		h.settle()
		if h.runner.callCount() != 0 {
			t.Error("the old 07:00 wake-up ran")
		}
		if _, err := h.svc.UpdateTask(bg, task.ID, UpdateRequest{ExpectedVersion: task.Version}); !errors.Is(err, ErrConflict) {
			t.Errorf("stale version: %v", err)
		}
	})
	t.Run("一時停止中は自動起動せず再開後は先の予定から", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		h.svc.SetEnabled(bg, task.ID, false)
		h.clock.Set(mustTime(t, "2026-10-30T07:30:00+09:00"))
		h.settle()
		got, _ := h.svc.GetTask(bg, task.ID)
		if h.runner.callCount() != 0 || got.State != TaskPaused || got.NextRunAt != nil {
			t.Fatalf("paused task ran or has a next run: %+v", got)
		}
		got, _ = h.svc.SetEnabled(bg, task.ID, true)
		if !got.NextRunAt.Equal(mustTime(t, "2026-10-31T07:00:00+09:00")) {
			t.Errorf("next after resume = %v", got.NextRunAt)
		}
	})
	t.Run("削除すると以後起動せず実行中のRunは止まる", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		block := make(chan struct{})
		h.runner.started = make(chan string, 1)
		h.runner.push(fakeStep{block: block})
		h.svc.RunNow(bg, task.ID, "")
		h.svc.Tick(bg)
		runID := <-h.runner.started
		if err := h.svc.DeleteTask(bg, task.ID); err != nil {
			t.Fatal(err)
		}
		h.svc.wg.Wait()
		var status string
		h.store.db.QueryRow(`SELECT status FROM runs WHERE id = ?`, runID).Scan(&status)
		if status != RunCancelled {
			t.Errorf("running run = %s", status)
		}
		h.clock.Set(mustTime(t, "2026-10-31T08:00:00+09:00"))
		h.settle()
		if h.runner.callCount() != 1 {
			t.Error("a deleted task ran")
		}
		if _, err := h.svc.GetTask(bg, task.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("deleted task: %v", err)
		}
	})
	t.Run("スリープ復帰では24時間以内の最新1回だけ補完し古い分はskippedに残す", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(func(r *CreateRequest) { r.Schedule = Schedule{Kind: ScheduleCron, Cron: "0 */6 * * *"} })
		// Asleep from 06:00 on the 30th until 13:00 on the 31st.
		h.clock.Set(mustTime(t, "2026-10-31T13:00:00+09:00"))
		h.settle()
		rs := h.runs(task.ID)
		if got := statuses(rs); strings.Join(got, ",") != "completed,skipped" {
			t.Fatalf("runs = %v", got)
		}
		if !rs[0].ScheduledFor.Equal(mustTime(t, "2026-10-31T12:00:00+09:00")) || rs[0].Triggers[0] != TriggerCatchUp {
			t.Errorf("made-up run = %+v", rs[0])
		}
		if !strings.Contains(rs[1].Reason, "4 scheduled run(s)") {
			t.Errorf("skip reason = %q", rs[1].Reason)
		}
		got, _ := h.svc.GetTask(bg, task.ID)
		if !got.NextRunAt.Equal(mustTime(t, "2026-10-31T18:00:00+09:00")) {
			t.Errorf("next = %v", got.NextRunAt)
		}
	})
	t.Run("24時間より古い予定だけなら補完せずskippedにする", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(func(r *CreateRequest) {
			r.Schedule = Schedule{Kind: ScheduleWeekly, Time: "07:00", Days: []string{"fri"}}
		})
		h.clock.Set(mustTime(t, "2026-11-01T09:00:00+09:00"))
		h.settle()
		if got := statuses(h.runs(task.ID)); strings.Join(got, ",") != "skipped" || h.runner.callCount() != 0 {
			t.Errorf("runs = %v, calls = %d", got, h.runner.callCount())
		}
	})
	t.Run("1日の起動上限を超えた自動起動はskippedとして理由を残す", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(func(r *CreateRequest) {
			r.Schedule = Schedule{Kind: ScheduleCron, Cron: "*/10 * * * *"}
			r.Limits.MaxRunsPerDay = 2
		})
		for i := 0; i < 4; i++ {
			h.clock.Add(10 * time.Minute)
			h.settle()
		}
		got := statuses(h.runs(task.ID))
		if strings.Join(got, ",") != "skipped,skipped,completed,completed" {
			t.Errorf("runs = %v", got)
		}
		if rs := h.runs(task.ID); !strings.Contains(rs[0].Reason, "daily limit") {
			t.Errorf("reason = %q", rs[0].Reason)
		}
	})
}

func TestSelfWake(t *testing.T) {
	t.Run("nextWakeAtは最短間隔を守り1件だけ置き換えて実行する", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(func(r *CreateRequest) { r.Schedule = Schedule{Kind: ScheduleManual} })
		now := h.clock.Now()
		h.runner.push(
			fakeStep{result: result(OutcomeUnchanged, "未確定", withWake(now.Add(5*time.Minute)))},
			fakeStep{result: result(OutcomeUnchanged, "未確定", withWake(now.Add(3*time.Hour)))},
			fakeStep{result: result(OutcomeUnchanged, "未確定", withWake(now.Add(2*time.Hour)))},
		)
		h.svc.RunNow(bg, task.ID, "")
		h.settle()
		if rs := h.runs(task.ID); len(rs[0].Warnings) == 0 || !strings.Contains(rs[0].Warnings[0], "at least") {
			t.Errorf("a 5 minute wake-up was accepted: %+v", rs[0])
		}
		h.svc.RunNow(bg, task.ID, "")
		h.settle()
		h.svc.RunNow(bg, task.ID, "")
		h.settle()
		ws, _ := h.svc.Wakeups(bg, task.ID)
		if len(ws) != 1 || ws[0].Kind != WakeSelf || !ws[0].DueAt.Equal(now.Add(2*time.Hour).Truncate(time.Second)) {
			t.Fatalf("wakeups = %+v", ws)
		}
		h.clock.Add(2*time.Hour + time.Second)
		h.settle()
		rs := h.runs(task.ID)
		if rs[0].Triggers[0] != TriggerSelf {
			t.Errorf("triggers = %v", rs[0].Triggers)
		}
	})
	t.Run("nullを返すと保留中の自己予約は取り消す", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		h.runner.push(fakeStep{result: result(OutcomeUnchanged, "後で", withWake(h.clock.Now().Add(time.Hour)))}, fakeStep{result: result(OutcomeChanged, "確定", withKey("done"))})
		h.svc.RunNow(bg, task.ID, "")
		h.settle()
		h.svc.RunNow(bg, task.ID, "")
		h.settle()
		ws, _ := h.svc.Wakeups(bg, task.ID)
		for _, w := range ws {
			if w.Kind == WakeSelf {
				t.Errorf("self wake-up kept: %+v", w)
			}
		}
	})
	t.Run("定期予定と近い自己予約は定期実行にまとめる", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil) // daily 07:00, now 06:00
		h.runner.push(fakeStep{result: result(OutcomeUnchanged, "後で", withWake(mustTime(t, "2026-10-30T07:05:00+09:00")))})
		h.svc.RunNow(bg, task.ID, "")
		h.settle()
		ws, _ := h.svc.Wakeups(bg, task.ID)
		if len(ws) != 1 || ws[0].Kind != WakeSchedule {
			t.Errorf("wakeups = %+v", ws)
		}
	})
	t.Run("期限後の自己予約は拒否する", func(t *testing.T) {
		h := newHarness(t)
		end := mustTime(t, "2026-10-30T09:00:00+09:00")
		task := h.create(func(r *CreateRequest) { r.Limits.ExpiresAt = &end })
		h.runner.push(fakeStep{result: result(OutcomeUnchanged, "後で", withWake(end.Add(time.Hour)))})
		h.svc.RunNow(bg, task.ID, "")
		h.settle()
		if rs := h.runs(task.ID); len(rs[0].Warnings) == 0 || !strings.Contains(rs[0].Warnings[0], "task ends") {
			t.Errorf("warnings = %v", rs[0].Warnings)
		}
	})
}

func TestRecovery(t *testing.T) {
	t.Run("プロンプト送信前に止まったRunは再キューして実行する", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		h.runner.push(fakeStep{noSend: true, err: errors.New("crash")})
		h.svc.RunNow(bg, task.ID, "")
		// Simulate a crash during preparation: mark running/preparing by hand.
		h.store.db.Exec(`UPDATE runs SET status = 'running', started_at = 1 WHERE task_id = ?`, task.ID)
		var runID string
		h.store.db.QueryRow(`SELECT id FROM runs WHERE task_id = ?`, task.ID).Scan(&runID)
		h.store.db.Exec(`INSERT INTO attempts (id, run_id, phase, provider, started_at) VALUES ('att_x', ?, 'preparing', 'claude', 1)`, runID)
		h.restart()
		h.runner.script = nil
		h.settle()
		rs := h.runs(task.ID)
		if len(rs) != 1 || rs[0].Status != RunCompleted {
			t.Errorf("runs = %+v", rs)
		}
	})
	t.Run("送信後に止まったRunは再送せずneeds_reconciliationにする", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		block := make(chan struct{})
		h.runner.started = make(chan string, 1)
		h.runner.push(fakeStep{block: block})
		h.svc.RunNow(bg, task.ID, "")
		h.svc.Tick(bg)
		<-h.runner.started
		h.restart() // shutdown cuts the agent off; Recover decides
		h.settle()
		rs := h.runs(task.ID)
		if len(rs) != 1 || rs[0].Status != RunNeedsReconciliation || rs[0].ErrorKind != ErrKindInterrupted {
			t.Fatalf("runs = %+v", rs)
		}
		if h.runner.callCount() != 1 {
			t.Errorf("the instruction was sent again (%d calls)", h.runner.callCount())
		}
		got, _ := h.svc.GetTask(bg, task.ID)
		if got.State != TaskNeedsAttention {
			t.Errorf("state = %s", got.State)
		}
		if ns := h.notifications(task.ID); len(ns) != 1 || ns[0].Kind != NotifyFailure {
			t.Errorf("notifications = %+v", ns)
		}
		if _, err := h.svc.DismissRun(bg, rs[0].ID); err != nil {
			t.Fatal(err)
		}
		close(block)
	})
	t.Run("保存済みの結果があれば再起動後にそれを取り込む", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		h.svc.RunNow(bg, task.ID, "")
		var runID string
		h.store.db.QueryRow(`SELECT id FROM runs WHERE task_id = ?`, task.ID).Scan(&runID)
		out := RunOutput{Structured: []byte(mustJSON(result(OutcomeChanged, "保存済み", withKey("saved"), withSet("k", "1"))))}
		h.store.db.Exec(`UPDATE runs SET status = 'running', started_at = 1, memory_snapshot = ? WHERE id = ?`, mustJSON(h.memory(task.ID)), runID)
		h.store.db.Exec(`INSERT INTO attempts (id, run_id, phase, provider, started_at, raw_result) VALUES ('att_y', ?, 'finished', 'claude', 1, ?)`, runID, mustJSON(out))
		h.restart()
		h.settle()
		rs := h.runs(task.ID)
		if rs[0].Status != RunCompleted || rs[0].Outcome != OutcomeChanged || h.runner.callCount() != 0 {
			t.Errorf("run = %+v calls=%d", rs[0], h.runner.callCount())
		}
		if string(h.memory(task.ID).State["k"]) != "1" || len(h.notifications(task.ID)) != 1 {
			t.Error("the saved result was not taken in")
		}
	})
}

func TestStopAndTimeout(t *testing.T) {
	t.Run("実行停止は当該Runだけを止め他タスクは続く", func(t *testing.T) {
		h := newHarness(t)
		h.svc.cfg.MaxConcurrentRuns = 2
		t1 := h.create(nil)
		t2 := h.create(func(r *CreateRequest) { r.Name = "他" })
		b1, b2 := make(chan struct{}), make(chan struct{})
		h.runner.started = make(chan string, 2)
		h.runner.push(fakeStep{block: b1}, fakeStep{block: b2, result: result(OutcomeUnchanged, "ok")})
		r1, _ := h.svc.RunNow(bg, t1.ID, "")
		h.svc.RunNow(bg, t2.ID, "")
		h.svc.Tick(bg)
		<-h.runner.started
		<-h.runner.started
		got, err := h.svc.CancelRun(bg, r1.Run.ID)
		if err != nil || got.Status != RunCancelled {
			t.Fatalf("cancel = %+v %v", got, err)
		}
		if a := h.svc.ActiveRuns(); len(a) != 1 {
			t.Errorf("active = %v", a)
		}
		close(b2)
		h.svc.wg.Wait()
		if rs := h.runs(t2.ID); rs[0].Status != RunCompleted {
			t.Errorf("other task's run = %s", rs[0].Status)
		}
		if len(h.notifications(t1.ID)) != 0 {
			t.Error("a run stopped by the user was notified as a failure")
		}
	})
	t.Run("時間制限を超えたRunはtimed_outで止める", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(func(r *CreateRequest) { r.Limits.MaxRunMinutes = 1 })
		block := make(chan struct{})
		defer close(block)
		h.runner.started = make(chan string, 1)
		h.runner.push(fakeStep{block: block})
		h.svc.RunNow(bg, task.ID, "")
		h.svc.Tick(bg)
		<-h.runner.started
		h.clock.Add(2 * time.Minute) // wall clock passes the deadline (e.g. after sleep)
		h.svc.Tick(bg)
		h.svc.wg.Wait()
		if rs := h.runs(task.ID); rs[0].Status != RunTimedOut {
			t.Errorf("run = %s", rs[0].Status)
		}
	})
}

func TestMemoryAndQuestions(t *testing.T) {
	t.Run("実行中にユーザーが編集したMemoryの値は古い結果で上書きしない", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		block := make(chan struct{})
		h.runner.started = make(chan string, 1)
		h.runner.push(fakeStep{block: block, result: result(OutcomeChanged, "x", withKey("m"), withSet("watch", `"main"`), withSet("other", "2"),
			func(r map[string]any) { r["memorySummary"] = "モデルの要約" })})
		h.svc.RunNow(bg, task.ID, "")
		h.svc.Tick(bg)
		<-h.runner.started
		m := h.memory(task.ID)
		summary := "ユーザーの要約"
		if _, err := h.svc.PutMemory(bg, task.ID, MemoryUpdate{ExpectedVersion: m.Version, Summary: &summary, State: rawState(map[string]string{"watch": `"release"`})}); err != nil {
			t.Fatal(err)
		}
		close(block)
		h.svc.wg.Wait()
		after := h.memory(task.ID)
		if string(after.State["watch"]) != `"release"` || string(after.State["other"]) != "2" || after.Summary != "ユーザーの要約" {
			t.Errorf("memory = %+v", after)
		}
		if rs := h.runs(task.ID); len(rs[0].Warnings) == 0 {
			t.Error("no warning about the kept edits")
		}
	})
	t.Run("Memoryの上限を超える編集は拒否する", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		big := strings.Repeat("あ", MaxSummaryBytes)
		if _, err := h.svc.PutMemory(bg, task.ID, MemoryUpdate{ExpectedVersion: 1, Summary: &big}); !errors.Is(err, ErrInvalid) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("質問はwaiting_userになり回答でチェックポイントから新しいRunを始める", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		h.runner.push(fakeStep{result: result(OutcomeBlocked, "PRのマージ可否を判断できません", withQuestion("マージしてよいですか？"),
			func(r map[string]any) { r["report"] = "途中経過: CIは成功" })})
		h.svc.RunNow(bg, task.ID, "")
		h.settle()
		got, _ := h.svc.GetTask(bg, task.ID)
		if got.State != TaskWaitingUser || got.PendingQuestion == nil {
			t.Fatalf("task = %+v", got)
		}
		if ns := h.notifications(task.ID); len(ns) != 1 || ns[0].Kind != NotifyQuestion {
			t.Errorf("notifications = %+v", ns)
		}
		if h.runner.callCount() != 1 {
			t.Fatal("the process kept waiting or ran again")
		}
		res, err := h.svc.AnswerQuestion(bg, got.PendingQuestion.ID, "マージしないで報告だけ", "ans-0001")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.svc.AnswerQuestion(bg, got.PendingQuestion.ID, "別の回答", "ans-0002"); !errors.Is(err, ErrConflict) {
			t.Errorf("second answer: %v", err)
		}
		h.settle()
		p := h.runner.lastPrompt()
		if !strings.Contains(p, "途中経過: CIは成功") || !strings.Contains(p, "マージしないで報告だけ") || !strings.Contains(p, "does not grant permissions") {
			t.Errorf("prompt lacks the checkpoint or the answer:\n%s", p)
		}
		if r, _ := h.svc.GetRun(bg, res.Run.ID); !contains(r.Triggers, TriggerAnswer) {
			t.Errorf("triggers = %v", r.Triggers)
		}
	})
	t.Run("期限切れの質問への回答は拒否する", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		h.runner.push(fakeStep{result: result(OutcomeBlocked, "判断待ち", withQuestion("続けますか？"))})
		h.svc.RunNow(bg, task.ID, "")
		h.settle()
		var qid string
		h.store.db.QueryRow(`SELECT id FROM questions`).Scan(&qid)
		h.clock.Add(8 * 24 * time.Hour)
		if _, err := h.svc.AnswerQuestion(bg, qid, "はい", ""); !errors.Is(err, ErrConflict) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("追加指示のRunは以前の会話とMemoryを参照できる", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		h.runner.push(fakeStep{result: result(OutcomeChanged, "PR #5 のCIが失敗", withKey("pr5"), withSet("failingPR", "5"),
			func(r map[string]any) { r["memorySummary"] = "PR #5 を追跡中" })})
		h.svc.RunNow(bg, task.ID, "")
		h.settle()
		h.svc.PostMessage(bg, task.ID, "詳しく調べて", "")
		h.settle()
		p := h.runner.lastPrompt()
		for _, want := range []string{"PR #5 のCIが失敗", "PR #5 を追跡中", `"failingPR": 5`, "詳しく調べて", "## 7. External data"} {
			if !strings.Contains(p, want) {
				t.Errorf("prompt lacks %q", want)
			}
		}
	})
}

func TestValidationAndArtifacts(t *testing.T) {
	t.Run("範囲外のcwd・未信頼フォルダ・未提供のモデルとeffortを理由付きで拒否する", func(t *testing.T) {
		h := newHarness(t)
		h.env.catalog = providers.ModelCatalog{Models: []providers.ModelOption{{ID: "sonnet", Efforts: []providers.EffortOption{{ID: "low"}, {ID: "high"}}}}}
		base := Settings{Name: "x", Instruction: "y", Provider: ProviderClaude, Cwd: h.cwd(), Schedule: Schedule{Kind: ScheduleManual}}
		try := func(mod func(*CreateRequest)) error {
			req := CreateRequest{Settings: base}
			mod(&req)
			_, err := h.svc.CreateTask(bg, req)
			return err
		}
		if err := try(func(r *CreateRequest) { r.Cwd = h.dir }); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "workspace roots") {
			t.Errorf("outside cwd: %v", err)
		}
		if err := try(func(r *CreateRequest) { r.Model = "gpt-5" }); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "not offered") {
			t.Errorf("unknown model: %v", err)
		}
		if err := try(func(r *CreateRequest) { r.Model, r.Effort = "sonnet", "max" }); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "low, high") {
			t.Errorf("unknown effort: %v", err)
		}
		if err := try(func(r *CreateRequest) { r.Schedule = Schedule{Kind: ScheduleCron, Cron: "61 * * * *"} }); !errors.Is(err, ErrInvalid) {
			t.Errorf("bad cron: %v", err)
		}
		if err := try(func(r *CreateRequest) { r.Permissions.AllowedTools = []string{"Bash"} }); !errors.Is(err, ErrInvalid) {
			t.Errorf("unrestricted Bash: %v", err)
		}
		if err := try(func(r *CreateRequest) {
			r.Provider = ProviderCodex
			r.Permissions.AllowedTools = []string{"Bash(ls:*)"}
		}); !errors.Is(err, ErrInvalid) {
			t.Errorf("codex rules: %v", err)
		}
		h.env.trusted = false
		if err := try(func(r *CreateRequest) {}); !errors.Is(err, ErrTrustRequired) {
			t.Errorf("untrusted: %v", err)
		}
		if err := try(func(r *CreateRequest) { r.Trust = true }); err != nil {
			t.Errorf("trust: true: %v", err)
		}
	})
	t.Run("成果物はタスクのフォルダ内のファイルだけ受け付ける", func(t *testing.T) {
		h := newHarness(t)
		task := h.create(nil)
		os.WriteFile(filepath.Join(h.cwd(), "report.md"), []byte("# r"), 0o644)
		os.WriteFile(filepath.Join(h.dir, "secret.txt"), []byte("s"), 0o644)
		h.runner.push(fakeStep{result: result(OutcomeChanged, "レポート作成", withKey("r"), func(r map[string]any) {
			r["artifacts"] = []any{map[string]any{"path": "report.md", "title": "レポート"}, map[string]any{"path": "../secret.txt", "title": "x"},
				map[string]any{"path": filepath.Join(h.dir, "secret.txt"), "title": "y"}}
		})})
		h.svc.RunNow(bg, task.ID, "")
		h.settle()
		arts, _ := h.svc.Artifacts(bg, task.ID)
		if len(arts) != 1 || arts[0].Path != filepath.Join(h.cwd(), "report.md") {
			t.Errorf("artifacts = %+v", arts)
		}
		if rs := h.runs(task.ID); len(rs[0].Warnings) != 2 {
			t.Errorf("warnings = %v", rs[0].Warnings)
		}
	})
}

// rawState builds a memory state from JSON texts.
func rawState(m map[string]string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range m {
		out[k] = json.RawMessage(v)
	}
	return out
}
