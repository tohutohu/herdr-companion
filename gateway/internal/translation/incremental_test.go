package translation

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/notifications"
)

func Testセッション一覧のプレビューは設定に従って非同期翻訳しキャッシュを共有する(t *testing.T) {
	s, calls := lifecycleService(t)
	if err := s.Start("0.8b"); err != nil {
		t.Fatal(err)
	}
	on, off := true, false
	original := model.Session{ID: "session", LastMessage: "Build succeeded."}
	if got := s.Session(original); !reflect.DeepEqual(got, original) {
		t.Fatal("global off ignored", got)
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Fatal("disabled preview queued inference", err)
	}
	waitFor(t, func() bool { return s.Session(original, &on).LastMessage == "ja:Build succeeded." })
	raw := []model.Message{{Role: model.RoleAssistant, Blocks: []model.Block{model.ReportBlock(original.LastMessage)}}}
	if got := s.Messages(original.ID, raw, false, &on); got[0].Blocks[0].Text != "ja:Build succeeded." {
		t.Fatal("preview cache not shared with conversation", got)
	}
	if err := s.Configure(false, true); err != nil {
		t.Fatal(err)
	}
	if got := s.Session(original); got.LastMessage != "ja:Build succeeded." {
		t.Fatal("global on ignored", got)
	}
	if got := s.Session(original, &off); !reflect.DeepEqual(got, original) {
		t.Fatal("session off ignored", got)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if got := s.Session(original, &on); got.LastMessage != "ja:Build succeeded." {
		t.Fatal("cached preview lost while stopped", got)
	}
	for _, preview := range []string{"Uncached preview", "完了しました。", ""} {
		if got := s.Session(model.Session{LastMessage: preview}, &on); got.LastMessage != preview {
			t.Fatal("unavailable translation must preserve preview", got)
		}
	}
	if original.LastMessage != "Build succeeded." {
		t.Fatal("provider preview mutated", original)
	}
	if data, _ := os.ReadFile(calls); string(data) != "x" {
		t.Fatalf("duplicate preview inference: %s", data)
	}
}

func Testターンの途中でも確定メッセージを翻訳しストリームの断片は翻訳しない(t *testing.T) {
	s, calls := lifecycleService(t)
	if err := s.Start("0.8b"); err != nil {
		t.Fatal(err)
	}
	on := true
	raw := []model.Message{
		{ID: "first", Role: model.RoleAssistant, Blocks: []model.Block{model.ReportBlock("First message.")}},
		{ID: "stream", Role: model.RoleAssistant, Streaming: true, Blocks: []model.Block{model.ReportBlock("Incomplete")}},
		{ID: "tool", Role: model.RoleTool, Blocks: []model.Block{model.TextBlock("go test")}},
	}
	first := s.Messages("session", raw, false, &on)
	if first[0].Blocks[0].Text != "翻訳中…" || first[1].Blocks[0].Text != "応答を受信中…" {
		t.Fatal(first)
	}
	for range 3 {
		waitFor(t, func() bool { return s.Messages("session", raw, false, &on)[0].Blocks[0].Text == "ja:First message." })
	}
	if data, _ := os.ReadFile(calls); string(data) != "x" {
		t.Fatalf("stream translated or duplicate inference: %s", data)
	}
	raw[1].Blocks[0].Text = "Complete second message."
	raw[1].Streaming = false
	waitFor(t, func() bool {
		return s.Messages("session", raw, false, &on)[1].Blocks[0].Text == "ja:Complete second message."
	})
	got := s.Messages("session", raw, false, &on)
	if got[0].ID != "first" || got[1].ID != "stream" || got[1].Blocks[0].OriginalText != "Complete second message." || got[2].Blocks[0].Text != "go test" {
		t.Fatal(got)
	}
	if raw[0].Blocks[0].Text != "First message." || raw[0].Blocks[0].OriginalText != "" {
		t.Fatal("provider cache changed")
	}
	if data, _ := os.ReadFile(calls); string(data) != "xx" {
		t.Fatalf("calls: %s", data)
	}
}

func Test通知は送信前に全表示文を翻訳し会話のキャッシュと原文を保持する(t *testing.T) {
	s, calls := lifecycleService(t)
	if err := s.Start("0.8b"); err != nil {
		t.Fatal(err)
	}
	on, off := true, false
	raw := []model.Message{{ID: "report", Role: model.RoleAssistant, Blocks: []model.Block{model.ReportBlock("Build succeeded.")}}}
	waitFor(t, func() bool { return s.Messages("session", raw, false, &on)[0].Blocks[0].Text == "ja:Build succeeded." })
	content := notifications.Content{Title: "Codex completed", Body: "Project\nBuild succeeded.", Report: &raw[0]}
	got := s.Notification(context.Background(), "session", content, &on)
	if got.Title != "ja:Codex completed" || got.Body != "ja:Project\nBuild succeeded." ||
		got.Report.Blocks[0].Text != "ja:Build succeeded." || got.Report.Blocks[0].OriginalText != "Build succeeded." || got.Report.ID != "report" {
		t.Fatal(got)
	}
	if data, _ := os.ReadFile(calls); string(data) != "xxx" {
		t.Fatalf("report was translated twice: %s", data)
	}
	if content.Report.Blocks[0].Text != "Build succeeded." {
		t.Fatal("source report mutated")
	}
	if got := s.Notification(context.Background(), "session", content, &off); got.Title != content.Title || got.Report != content.Report {
		t.Fatal("session off ignored")
	}
	// Resident notifications inherit the global display setting.
	if err := s.Configure(false, true); err != nil {
		t.Fatal(err)
	}
	if got := s.Notification(context.Background(), "", content); got.Title != "ja:Codex completed" {
		t.Fatal(got)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if got := s.Notification(context.Background(), "session", content, &on); got.Report.Blocks[0].Text != "ja:Build succeeded." {
		t.Fatal("cached report lost while stopped")
	}
	missing := notifications.Content{Title: "Uncached title", Body: "Uncached body"}
	if got := s.Notification(context.Background(), "session", missing, &on); got != missing {
		t.Fatal("failure must preserve notification", got)
	}
}
