package translation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/notifications"
)

type contextConversation struct {
	messages map[string][]model.Message
	calls    int
	err      error
}

func (r *contextConversation) Conversation(_ context.Context, id string) (model.Session, []model.Message, error) {
	r.calls++
	return model.Session{}, r.messages[id], r.err
}

func workerRequests(t *testing.T, calls string) []map[string]string {
	t.Helper()
	data, err := os.ReadFile(calls + ".requests")
	if err != nil {
		t.Fatal(err)
	}
	var requests []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var request map[string]string
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, request)
	}
	return requests
}

func Test文脈は確定した会話の直前だけを上限付きで抽出する(t *testing.T) {
	messages := []model.Message{
		{Role: model.RoleUser, Blocks: []model.Block{model.TextBlock("古い依頼")}},
		{Role: model.RoleUser, Blocks: []model.Block{model.TextBlock("Mac UIも更新して")}},
		{Role: model.RoleAssistant, Blocks: []model.Block{model.ReportBlock("Compose Desktopを再ビルドします。\n```sh\nsecret-command\n```\nGatewayは別アプリです。"), model.TextBlock("private reasoning")}},
		{Role: model.RoleTool, Blocks: []model.Block{model.TextBlock("private tool output")}},
		{Role: model.RoleAssistant, Streaming: true, Blocks: []model.Block{model.ReportBlock("unfinished")}},
		{Role: model.RoleUser, Queued: true, Blocks: []model.Block{model.TextBlock("queued")}},
		{Role: model.RoleAssistant, Blocks: []model.Block{model.ReportBlock("current report")}},
		{Role: model.RoleUser, Blocks: []model.Block{model.TextBlock("future request")}},
	}
	got := contextBefore(messages, 6, 0)
	for _, want := range []string{"user: Mac UIも更新して", "assistant: Compose Desktop", "Gatewayは別アプリ"} {
		if !strings.Contains(got, want) {
			t.Fatal(got)
		}
	}
	for _, excluded := range []string{"古い依頼", "secret-command", "private", "unfinished", "queued", "current", "future"} {
		if strings.Contains(got, excluded) {
			t.Fatalf("included %q: %s", excluded, got)
		}
	}
	if got != contextBefore(messages[:7], 6, 0) {
		t.Fatal("future changed old context")
	}
	long := "主題" + strings.Repeat("日本語", 5000) + "最後の提案"
	bounded := contextProse(long)
	if !utf8.ValidString(bounded) || len([]rune(bounded)) > 810 || !strings.HasPrefix(bounded, "主題") || !strings.HasSuffix(bounded, "最後の提案") {
		t.Fatal(bounded)
	}
	for _, code := range []string{"```go\nsecret", "    secret\n\tcode", `{"secret":"json"}`} {
		if contextProse(code) != "" {
			t.Fatal(code)
		}
	}
}

func Test入力の文脈をセッションごとに取得し異なる文脈のキャッシュを分離する(t *testing.T) {
	s, calls := lifecycleService(t)
	r := &contextConversation{messages: map[string][]model.Message{
		"a": {{Role: model.RoleAssistant, Blocks: []model.Block{model.ReportBlock("Rebuild Compose Desktop?")}}},
		"b": {{Role: model.RoleAssistant, Blocks: []model.Block{model.ReportBlock("Restart the Gateway?")}}},
	}}
	s.conversations = r
	if err := s.Start("0.8b"); err != nil {
		t.Fatal(err)
	}
	on, off := true, false
	for _, session := range []string{"a", "b", "a"} {
		if got, err := s.Input(context.Background(), session, "それでお願い", &on); err != nil || got != "en:それでお願い" {
			t.Fatal(got, err)
		}
	}
	requests := workerRequests(t, calls)
	if len(requests) != 2 || !strings.Contains(requests[0]["context"], "Compose Desktop") || strings.Contains(requests[0]["context"], "Gateway") || !strings.Contains(requests[1]["context"], "Gateway") {
		t.Fatal(requests)
	}
	before := r.calls
	for _, text := range []string{"Hello", "/command 日本語"} {
		if _, err := s.Input(context.Background(), "a", text, &on); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = s.Input(context.Background(), "a", "翻訳しない", &off)
	if r.calls != before {
		t.Fatal("unused history fetched")
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	restored := New(ctx, s.dir, r)
	if got, err := restored.Input(ctx, "a", "それでお願い", &on); err != nil || got != "en:それでお願い" {
		t.Fatal("context cache lost after restart", got, err)
	}
}

func Test履歴取得失敗と新規セッションでは文脈なしで入力を翻訳する(t *testing.T) {
	s, calls := lifecycleService(t)
	r := &contextConversation{err: errors.New("history unavailable")}
	s.conversations = r
	if err := s.Start("0.8b"); err != nil {
		t.Fatal(err)
	}
	on := true
	for _, session := range []string{"", "missing"} {
		if _, err := s.Input(context.Background(), session, "変更して", &on); err != nil {
			t.Fatal(err)
		}
	}
	requests := workerRequests(t, calls)
	if len(requests) != 1 || requests[0]["context"] != "" || r.calls != 1 {
		t.Fatal(requests, r.calls)
	}
}

func Test報告と通知は同じ時点の文脈で訳を共有し後続発言で再翻訳しない(t *testing.T) {
	s, calls := lifecycleService(t)
	raw := []model.Message{
		{ID: "request", Role: model.RoleUser, Blocks: []model.Block{model.TextBlock("Mac UIをビルドして")}},
		{ID: "report", Role: model.RoleAssistant, Blocks: []model.Block{model.ReportBlock("Build succeeded."), model.ReportBlock("You can open it now.")}},
	}
	r := &contextConversation{messages: map[string][]model.Message{"session": raw}}
	s.conversations = r
	if err := s.Start("0.8b"); err != nil {
		t.Fatal(err)
	}
	on := true
	waitFor(t, func() bool {
		return s.Messages("session", raw, false, &on)[1].Blocks[1].Text == "ja:You can open it now."
	})
	requests := workerRequests(t, calls)
	if len(requests) != 2 || !strings.Contains(requests[0]["context"], "Mac UI") || strings.Contains(requests[0]["context"], "Build succeeded") || !strings.Contains(requests[1]["context"], "Build succeeded") {
		t.Fatal(requests)
	}
	future := append(append([]model.Message(nil), raw...), model.Message{Role: model.RoleUser, Blocks: []model.Block{model.TextBlock("別の作業です")}})
	if got := s.Messages("session", future, false, &on); got[1].Blocks[0].Text != "ja:Build succeeded." {
		t.Fatal(got)
	}
	content := notifications.Content{Report: &raw[1]}
	got := s.Notification(context.Background(), "session", content, &on)
	if got.Report.Blocks[1].Text != "ja:You can open it now." || got.Report.Blocks[0].OriginalText != "Build succeeded." {
		t.Fatal(got)
	}
	if len(workerRequests(t, calls)) != 2 {
		t.Fatal("notification or future messages repeated inference")
	}
	if raw[1].Blocks[0].Text != "Build succeeded." {
		t.Fatal("provider history changed")
	}
}

func Test通知で結合された報告も元のブロックの文脈とキャッシュを使う(t *testing.T) {
	s, calls := lifecycleService(t)
	raw := []model.Message{
		{Role: model.RoleUser, Blocks: []model.Block{model.TextBlock("ビルドして")}},
		{ID: "report", Role: model.RoleAssistant, Blocks: []model.Block{
			model.ReportBlock("\nBuild succeeded.\n"), {Type: model.BlockImage}, model.ReportBlock("You can open it now."),
		}},
	}
	s.conversations = &contextConversation{messages: map[string][]model.Message{"session": raw}}
	if err := s.Start("0.8b"); err != nil {
		t.Fatal(err)
	}
	on := true
	waitFor(t, func() bool {
		return s.Messages("session", raw, false, &on)[1].Blocks[2].Text == "ja:You can open it now."
	})
	report, ok := notifications.LastReport(raw)
	if !ok {
		t.Fatal("missing report")
	}
	content := s.Notification(context.Background(), "session", notifications.Content{Report: &report}, &on)
	if len(content.Report.Blocks) != 1 || content.Report.Blocks[0].Text != "ja:\nBuild succeeded.\n\nja:You can open it now." || content.Report.Blocks[0].OriginalText != "Build succeeded.\n\nYou can open it now." {
		t.Fatal(content.Report)
	}
	if len(workerRequests(t, calls)) != 2 {
		t.Fatal("merged report was translated again")
	}
}
