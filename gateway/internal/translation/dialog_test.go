package translation

import (
	"context"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

func Test承認の原文と操作値を保持しコマンドを翻訳しない(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(ctx, t.TempDir())
	s.status.State = "ready"
	s.status.Output = true
	for source, target := range map[string]string{"Allow command?": "コマンドを許可しますか？", "Needs write permission": "書き込み権限が必要です", "Choose": "選択してください", "A": "同じ表示", "B": "同じ表示"} {
		s.cache[cacheKey(s.status.Model, source)] = cached{text: target}
	}
	ia := &model.Interaction{ID: "approval-id", Type: model.InteractionApproval, State: model.InteractionPending, Title: "Allow command?", Detail: "rm -rf /tmp/example\nNeeds write permission", DetailProse: "Needs write permission", Decisions: []string{model.DecisionApprove, model.DecisionDeny}, Questions: []model.Question{{ID: "question-id", Type: model.QuestionSelect, Question: "Choose", Options: []model.Option{{Label: "A", Preview: "code()"}, {Label: "B"}}}}}
	raw := []model.Message{{Role: model.RoleAssistant, Blocks: []model.Block{{Type: model.BlockInteraction, Interaction: ia}}}}
	got := s.Messages("session", raw, false)[0].Blocks[0].Interaction
	if got.Translation == nil {
		t.Fatal("no translated view")
	}
	tr := got.Translation
	if tr.ID != ia.ID || tr.Questions[0].ID != "question-id" || tr.Decisions[0] != model.DecisionApprove {
		t.Fatal("control identity changed")
	}
	if tr.Questions[0].Options[0].Value != "A" || tr.Questions[0].Options[1].Value != "B" || tr.Questions[0].Options[0].Preview != "code()" {
		t.Fatal("option values changed")
	}
	if tr.Detail != "rm -rf /tmp/example\nNeeds write permission\n\n書き込み権限が必要です" {
		t.Fatal(tr.Detail)
	}
	if got.Title != "Allow command?" || ia.Translation != nil || ia.Questions[0].Options[0].Value != "" {
		t.Fatal("original was mutated")
	}
	if len(s.pending) != 0 {
		t.Fatal("command unexpectedly queued", s.pending)
	}
}

func Testセッションタイトルを表示だけ翻訳して原文を保持する(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(ctx, t.TempDir())
	s.status.State = "ready"
	s.status.Output = true
	s.cache[cacheKey(s.status.Model, "Fix login")] = cached{text: "ログインを修正"}
	original := model.Session{ID: "session", Title: "Fix login"}
	got := s.Session(original)
	if got.Title != "ログインを修正" || got.OriginalTitle != "Fix login" || original.Title != "Fix login" {
		t.Fatal(got, original)
	}
	s.status.Output = false
	if got := s.Session(original); got.Title != "Fix login" || got.OriginalTitle != "" {
		t.Fatal(got)
	}
}
