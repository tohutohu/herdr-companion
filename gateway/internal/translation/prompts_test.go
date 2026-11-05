package translation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

func Test秒精度の履歴でも入力原文を復元し後の同じ英訳と取り違えない(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	s := New(ctx, dir)
	base := time.Date(2026, 11, 6, 8, 3, 0, 0, time.UTC)
	const sent = "Translation mode test"
	if err := s.RememberPrompt("codex:test", "翻訳モードのテスト", sent); err != nil {
		t.Fatal(err)
	}
	// Both inputs finish translating within the second their turns begin.
	saved := []sourcePrompt{
		{Source: "翻訳モードのテスト", Translated: sent, Created: base.Add(700 * time.Millisecond)},
		{Source: "翻訳モードをテスト", Translated: sent, Created: base.Add(2*time.Second + 300*time.Millisecond)},
	}
	data, _ := json.Marshal(saved)
	if err := os.WriteFile(filepath.Join(dir, "prompts", cacheKey("input:codex:test", sent)+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	restored := New(ctx, dir)
	if !restored.Status().InputEverEnabled {
		t.Fatal("session-only originals lost after restart")
	}
	off := false
	for _, tt := range []struct {
		at   time.Time
		want string
	}{
		{base.Add(-time.Second), sent},
		{base, saved[0].Source},
		{base.Add(500 * time.Millisecond), sent},
		{base.Add(700 * time.Millisecond), saved[0].Source},
		{base.Add(2 * time.Second), saved[1].Source},
	} {
		raw := []model.Message{{Role: model.RoleUser, Timestamp: tt.at, Blocks: []model.Block{model.TextBlock(sent)}}}
		got := restored.Messages("codex:test", raw, true, &off)[0].Blocks[0]
		if got.Text != tt.want || (tt.want != sent && got.OriginalText != sent) {
			t.Fatalf("at %v: %+v", tt.at, got)
		}
		if raw[0].Blocks[0].Text != sent || raw[0].Blocks[0].OriginalText != "" {
			t.Fatal("provider history mutated")
		}
	}
	other := restored.Messages("codex:other", []model.Message{{Role: model.RoleUser, Blocks: []model.Block{model.TextBlock(sent)}}}, true)
	if other[0].Blocks[0].Text != sent {
		t.Fatal("another session's source was used")
	}
}

func Test同じ本文への翻訳には原文切替を付けない(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(ctx, t.TempDir())
	on := true
	const text = "```go\nfmt.Println(1)\n```"
	s.cache[translationKey(s.status.Model, "ja", text)] = cached{Text: text}
	raw := []model.Message{{Role: model.RoleAssistant, Blocks: []model.Block{model.ReportBlock(text)}}}
	got := s.Messages("session", raw, true, &on)[0].Blocks[0]
	if got.Text != text || got.OriginalText != "" {
		t.Fatal(got)
	}
}

func Test日本語だけの報告を再翻訳して言い換えない(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(ctx, t.TempDir())
	on := true
	const text = "こちらのプロンプトの原文を見られなくなってる気がする"
	s.cache[translationKey(s.status.Model, "ja", text)] = cached{Text: "このプロンプトの原文が見られなくなった気がします。"}
	raw := []model.Message{{Role: model.RoleAssistant, Blocks: []model.Block{model.ReportBlock(text)}}}
	got := s.Messages("session", raw, true, &on)[0].Blocks[0]
	if got.Text != text || got.OriginalText != "" {
		t.Fatal(got)
	}
}
