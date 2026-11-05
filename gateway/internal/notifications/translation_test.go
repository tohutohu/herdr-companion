package notifications

import (
	"context"
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

func Test通知の全本文を翻訳してから切り詰め全端末へ同じ訳を送る(t *testing.T) {
	fs := &fakeSessions{list: []model.Session{sess(model.StatusRunning)}}
	sender := &fakeSender{}
	w, cfg := newWatcher(t, fs, sender)
	cfg.UpsertDevice("tablet", "tok-2")
	full := strings.Repeat("Original report. ", 500)
	w.Conversations = &fakeConversations{msgs: []model.Message{{ID: "report", Role: model.RoleAssistant, Blocks: []model.Block{model.ReportBlock(full)}}}}
	calls := 0
	w.Prepare = func(_ context.Context, id string, content Content) Content {
		calls++
		if id != "claude:s1" || content.Report == nil || content.Report.Blocks[0].Text != strings.TrimSpace(full) || len(sender.sent) != 0 {
			t.Fatal("must prepare full report before sending", content)
		}
		content.Title, content.Body = "Claude Code 完了", "認証処理の修正が完了しました"
		content.Report.Blocks = []model.Block{{Type: model.BlockText, Text: strings.Repeat("修正しました。\n", 500), OriginalText: full}}
		return content
	}
	w.Evaluate(context.Background())
	fs.list = []model.Session{sess(model.StatusCompleted)}
	w.Evaluate(context.Background())
	if calls != 1 || len(sender.sent) != 2 {
		t.Fatal(calls, len(sender.sent))
	}
	for _, sent := range sender.sent {
		if sent.data["title"] != "Claude Code 完了" || sent.data["body"] != "認証処理の修正が完了しました" || sent.data["sessionId"] != "claude:s1" || sent.data["canSend"] != "true" {
			t.Fatal(sent.data)
		}
		if payloadSize(sent.data) > maxPayloadBytes {
			t.Fatal("payload exceeded FCM limit")
		}
		report := decodeReport(t, sent.data)
		if report.ID != "report" || !strings.HasPrefix(report.Blocks[0].Text, "修正") || report.Blocks[0].OriginalText == "" {
			t.Fatal(report)
		}
	}
}

func Test原文付きの長い日本語通知もFCMの上限を守る(t *testing.T) {
	data := Payload(sess(model.StatusCompleted), model.StatusCompleted)
	report := model.Message{ID: "report", Role: model.RoleAssistant, Blocks: []model.Block{{Type: model.BlockText,
		Text: strings.Repeat("日本語の報告<&>\"です。\n", 300), OriginalText: strings.Repeat("English original report.\n", 400)}}}
	if !AttachReport(data, report) || payloadSize(data) > maxPayloadBytes {
		t.Fatal("oversize or missing report")
	}
	got := decodeReport(t, data)
	if !strings.HasSuffix(got.Blocks[0].Text, "…") || !strings.HasSuffix(got.Blocks[0].OriginalText, "…") {
		t.Fatal(got)
	}
}

func Test通知の翻訳待ちでも後続イベントを記録し報告は各イベントの時点を保つ(t *testing.T) {
	fs := &fakeSessions{list: []model.Session{sess(model.StatusRunning)}}
	w, _ := newWatcher(t, fs, &fakeSender{})
	queue := make(chan pendingPush, 4)
	w.pushQueues = []chan pendingPush{queue} // no consumer: translation is still occupied
	conv := &fakeConversations{msgs: []model.Message{{ID: "first", Role: model.RoleAssistant, Blocks: []model.Block{model.ReportBlock("First report")}}}}
	w.Conversations = conv
	ctx := context.Background()
	w.Evaluate(ctx)
	fs.list = []model.Session{sess(model.StatusCompleted)}
	w.Evaluate(ctx)
	fs.list = []model.Session{sess(model.StatusRunning)}
	w.Evaluate(ctx)
	conv.msgs = []model.Message{{ID: "second", Role: model.RoleAssistant, Blocks: []model.Block{model.ReportBlock("Second report")}}}
	fs.list = []model.Session{sess(model.StatusCompleted)}
	w.Evaluate(ctx)
	if len(queue) != 2 {
		t.Fatal("status observation blocked or lost a turn", len(queue))
	}
	first, second := <-queue, <-queue
	if first.content.Report.ID != "first" || second.content.Report.ID != "second" {
		t.Fatal("queued report was replaced by a later turn")
	}
}

func Test受信中の断片を通知の報告に使わない(t *testing.T) {
	msgs := []model.Message{
		{ID: "done", Role: model.RoleAssistant, Blocks: []model.Block{model.ReportBlock("Finished")}},
		{ID: "partial", Role: model.RoleAssistant, Streaming: true, Blocks: []model.Block{model.ReportBlock("Half a")}},
	}
	if report, ok := LastReport(msgs); !ok || report.ID != "done" {
		t.Fatal(report)
	}
}
