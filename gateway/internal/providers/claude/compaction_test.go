package claude

import (
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

func TestClaudeのコンパクション要約とサブエージェント報告は折りたたみ表示になる(t *testing.T) {
	tr, rec, ws := loadFixture(t, "compaction.jsonl")
	working := &providers.Live{PaneID: "w1:p1", HerdrStatus: herdr.StatusWorking}
	msgs := tr.Messages(ParseOptions{SessionID: "claude:test", Root: ws, Live: working, Sink: rec})
	assertGolden(t, "compaction.golden.json", msgs)
	if len(rec.Entries) != 0 {
		t.Errorf("dead letters = %+v, want none", rec.Entries)
	}
	for _, m := range msgs {
		if m.Role != model.RoleUser {
			continue
		}
		if text := m.Blocks[0].Text; text != "ログイン処理を調べて直して" {
			t.Errorf("user message %q: only the real prompt is the user's", text)
		}
	}
	if s := tr.summary(ParseOptions{}); s.LastMessage != "`exp` の検証を追加しました。" {
		t.Errorf("last message = %q", s.LastMessage)
	}
}

func Testコンパクション直後のコンテキスト使用量は圧縮後の大きさになる(t *testing.T) {
	tr, _, _ := loadFixture(t, "compaction.jsonl")
	if c := tr.summary(ParseOptions{}).Context; c == nil || c.UsedTokens != 12000 {
		t.Errorf("context = %+v, want 12000 tokens left by the compaction", c)
	}
}

func Testエージェントからのメッセージは枠を外して本文だけにする(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "サブエージェントの最終報告",
			in: "<agent-message from=\"abc\">\n[Subagent hand-back] The report follows:\n" +
				"  ## 結果\n  \n  - 直した\n</agent-message>",
			want: "Subagent report\n## 結果\n\n- 直した",
		},
		{
			name: "報告以外のメッセージ",
			in:   "<agent-message from=\"abc\">レビューをお願いします</agent-message>",
			want: "Agent message\nレビューをお願いします",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role, got := userText(tt.in)
			if role != model.RoleTool {
				t.Errorf("role = %q, want %q", role, model.RoleTool)
			}
			if got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}
		})
	}
}

func Testコンパクション要約から前置きの定型文を外す(t *testing.T) {
	tr, _, _ := loadFixture(t, "compaction.jsonl")
	for _, m := range tr.Messages(ParseOptions{}) {
		if text := m.Blocks[0].Text; strings.HasPrefix(text, "Compaction summary") {
			if strings.Contains(text, "being continued") || !strings.Contains(text, "Primary Request") {
				t.Errorf("summary = %q", text)
			}
			return
		}
	}
	t.Error("compaction summary not shown")
}
