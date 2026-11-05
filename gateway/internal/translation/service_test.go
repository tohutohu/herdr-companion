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
	if got := s.Messages("test-session", original, false); got[0].Blocks[0].Text != "Hello" {
		t.Fatal(got)
	}
	if len(s.queue) != 0 {
		t.Fatal("running turn was queued")
	}
	deadline := time.Now().Add(time.Second)
	for {
		got := s.Messages("test-session", original, true)
		if got[0].Blocks[0].OriginalText == "Hello" {
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
	if err := s.Start("0.8b"); err != nil {
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
		if strings.Contains(tt.text, "```go") && !strings.Contains(translated, "```go\nfmt.Println(\"hello\")\n```") {
			t.Fatal("code changed")
		}
	}
	fmt.Fprintln(os.Stderr, "PLaMo smoke test passed")
}
