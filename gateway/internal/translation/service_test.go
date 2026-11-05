package translation

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

func Test翻訳ワーカープロトコル(t *testing.T) {
	if os.Getenv("HERDR_TRANSLATION_HELPER") == "1" {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			var req map[string]string
			_ = json.Unmarshal(sc.Bytes(), &req)
			_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"text": req["target"] + ":" + req["text"]})
		}
		os.Exit(0)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(ctx, t.TempDir())
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^Test翻訳ワーカープロトコル$")
	cmd.Env = append(os.Environ(), "HERDR_TRANSLATION_HELPER=1")
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	s.mu.Lock()
	s.worker = &worker{cmd: cmd, in: in, out: bufio.NewReader(out)}
	s.status.State = "ready"
	s.status.Output = true
	s.status.Input = true
	s.status.InputEverEnabled = true
	s.mu.Unlock()
	for _, tt := range []struct{ in, want string }{{"変更して", "en:変更して"}, {"hello", "hello"}, {"/command 日本語", "/command 日本語"}} {
		got, err := s.Input(ctx, "test-session", tt.in)
		if err != nil || got != tt.want {
			t.Fatal(got, err)
		}
	}
	restored := New(ctx, s.dir)
	restored.status.InputEverEnabled = true
	source := restored.Messages("test-session", []model.Message{{Role: model.RoleUser, Timestamp: time.Now(), Blocks: []model.Block{model.TextBlock("en:変更して")}}}, false)
	if source[0].Blocks[0].Text != "変更して" {
		t.Fatal(source)
	}
	original := []model.Message{
		{Role: model.RoleAssistant, Blocks: []model.Block{model.ReportBlock("Hello"), model.TextBlock("Read file.txt"), {Type: model.BlockInteraction}}},
		{Role: model.RoleUser, Blocks: []model.Block{model.ReportBlock("日本語プロンプト")}},
		{Role: model.RoleTool, Blocks: []model.Block{model.TextBlock("tool output")}},
	}
	if got := s.Messages("test-session", original, false); got[0].Blocks[0].Text != "翻訳中…" || got[0].Blocks[0].OriginalText != "Hello" {
		t.Fatal(got)
	}
	deadline := time.Now().Add(time.Second)
	for {
		got := s.Messages("test-session", original, false)
		if got[0].Blocks[0].Text != "翻訳中…" {
			if got[0].Blocks[0].Text != "ja:Hello" {
				t.Fatal(got)
			}
			if got[0].Blocks[1].Text != "Read file.txt" || got[0].Blocks[1].OriginalText != "" || got[1].Blocks[0].Text != "日本語プロンプト" || got[2].Blocks[0].Text != "tool output" {
				t.Fatal("non-report translated", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("translation not completed")
		}
		time.Sleep(time.Millisecond)
	}
	if original[0].Blocks[0].Text != "Hello" || original[0].Blocks[0].OriginalText != "" {
		t.Fatal("provider cache mutated")
	}
	if err := s.Configure(false, false); err != nil {
		t.Fatal(err)
	}
	if got := s.Messages("test-session", original, true); got[0].Blocks[0].Text != "Hello" {
		t.Fatal(got)
	}
}
func Test翻訳モードを保存し未知のモデルを拒否する(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	s := New(ctx, dir)
	if err := s.Configure(true, true); err != nil {
		t.Fatal(err)
	}
	restored := New(ctx, dir)
	if !restored.Status().Output {
		t.Fatal("mode not persisted")
	}
	if err := s.Start("31b"); err == nil {
		t.Fatal("unknown model accepted")
	}
}
func Testローカル実モデル翻訳(t *testing.T) {
	dir := os.Getenv("HERDR_PLAMO_SMOKE_DIR")
	if dir == "" {
		t.Skip("opt-in: downloads the 0.8B model and MLX")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	s := New(ctx, dir)
	defer s.Close()
	size := os.Getenv("HERDR_PLAMO_SMOKE_MODEL")
	if size == "" {
		size = "0.8b"
	}
	if err := s.Start(size); err != nil {
		t.Fatal(err)
	}
	last := ""
	for {
		st := s.Status()
		if st.Detail != last {
			t.Log(st.Detail)
			last = st.Detail
		}
		if st.State == "failed" {
			t.Fatal(st.Error)
		}
		if st.State == "ready" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	for _, tt := range []struct{ text, target string }{{"会議は来週の月曜日に変更されました。", "en"}, {"The build succeeded.\n\n```go\nfmt.Println(\"hello\")\n```\nSee `README.md` for details.", "ja"}} {
		translated, err := s.Translate(ctx, tt.text, tt.target)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %s", tt.target, translated)
		if translated == tt.text {
			t.Fatal("unchanged translation")
		}
		if again, err := s.Translate(ctx, tt.text, tt.target); err != nil || again != translated {
			t.Fatal("cached result changed", again, err)
		}
		if strings.Contains(tt.text, "```go") && !strings.Contains(translated, "```go\nfmt.Println(\"hello\")\n```") {
			t.Fatal("code changed")
		}
	}
	fmt.Fprintln(os.Stderr, "PLaMo smoke test passed")
}

func Testセッションの翻訳選択はグローバル設定を上書きする(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(ctx, t.TempDir())
	on, off := true, false
	s.status.Input, s.status.Output = true, true
	if got, err := s.Input(ctx, "off", "修正して", &off); err != nil || got != "修正して" {
		t.Fatal(got, err)
	}
	s.status.Input, s.status.Output = false, false
	if _, err := s.Input(ctx, "on", "修正して", &on); err == nil {
		t.Fatal("stopped model silently sent original")
	}
	s.status.State = "ready"
	s.cache[translationKey(s.status.Model, "ja", "Hello")] = cached{Text: "こんにちは"}
	session := model.Session{ID: "session", Title: "Hello"}
	msgs := []model.Message{{Role: model.RoleAssistant, Blocks: []model.Block{model.ReportBlock("Hello")}}}
	for _, enabled := range []bool{false, true} {
		s.status.Output = !enabled
		got := s.Session(session, &enabled)
		display := s.Messages("session", msgs, true, &enabled)
		want := "Hello"
		if enabled {
			want = "こんにちは"
		}
		if got.Title != want || display[0].Blocks[0].Text != want {
			t.Fatal(got, display)
		}
	}
	if session.Title != "Hello" || msgs[0].Blocks[0].Text != "Hello" {
		t.Fatal("provider data mutated")
	}
}
