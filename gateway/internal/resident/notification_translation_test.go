package resident

import (
	"context"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/notifications"
)

func Test常駐通知も配信前に翻訳し保存済みの結果と通知IDは変えない(t *testing.T) {
	h := newHarness(t)
	task := h.create(nil)
	calls := 0
	h.svc.Notifier.Prepare = func(_ context.Context, id string, content notifications.Content) notifications.Content {
		calls++
		if id != "" || content.Body != "CI recovered" {
			t.Fatal(id, content)
		}
		content.Title, content.Body = "CI監視", "CIが復旧しました"
		return content
	}
	h.runner.push(fakeStep{result: result(OutcomeChanged, "CI recovered", withKey("ci:recovered"))})
	h.clock.Set(mustTime(t, "2026-10-30T07:00:10+09:00"))
	h.settle()
	entries := h.notifications(task.ID)
	if calls != 1 || len(entries) != 1 || h.sender.count() != 1 {
		t.Fatal(calls, entries)
	}
	push := h.sender.sent[0]
	if push["title"] != "CI監視" || push["body"] != "CIが復旧しました" || push["notificationId"] != entries[0].ID || push["taskId"] != task.ID {
		t.Fatal(push)
	}
	if entries[0].Body != "CI recovered" || entries[0].Status != NotifSent {
		t.Fatal("persisted notification changed", entries)
	}
}

func Test常駐通知の翻訳待ちでも次の実行を開始できる(t *testing.T) {
	h := newHarness(t)
	h.svc.cfg.Tick = 5 * time.Millisecond
	task := h.create(nil)
	entered := make(chan struct{}, 1)
	h.svc.Notifier.Prepare = func(ctx context.Context, _ string, content notifications.Content) notifications.Content {
		entered <- struct{}{}
		<-ctx.Done() // translation remains occupied until this test shuts down
		return content
	}
	h.runner.push(fakeStep{result: result(OutcomeChanged, "First report", withKey("first"))})
	if _, err := h.svc.RunNow(bg, task.ID, "translation-first"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.svc.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("notification did not enter translation")
	}
	if _, err := h.svc.RunNow(bg, task.ID, "translation-second"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		runs := h.runs(task.ID)
		if len(runs) == 2 && runs[0].Status == RunCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scheduler waited for translation", runs)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if h.sender.count() != 0 {
		t.Fatal("push was sent before translation finished")
	}
}
