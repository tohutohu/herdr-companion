package translation

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func longReply() string {
	paragraph := strings.Repeat("The gateway restarted the worker after the deadline. ", 12) + "\n\n"
	code := "```go\n" + strings.Repeat("fmt.Println(\"keep this line intact\")\n", 80) + "```\n\n"
	return strings.Repeat(paragraph, 10) + code + strings.Repeat(paragraph, 10) + "Done."
}

func Test長い返答は上限以下に分割し連結すると元に戻る(t *testing.T) {
	text := longReply()
	parts := segments(text)
	if len(parts) < 5 {
		t.Fatalf("not split: %d", len(parts))
	}
	if strings.Join(parts, "") != text {
		t.Fatal("segments do not restore the text")
	}
	for _, part := range parts {
		if strings.Count(part, "```")%2 != 0 {
			t.Fatalf("fenced code split: %q", part)
		}
		if len(part) > segmentBytes && !strings.Contains(part, "```") {
			t.Fatalf("prose segment exceeds limit: %d", len(part))
		}
		if !strings.HasSuffix(part, "\n\n") && part != parts[len(parts)-1] && !strings.HasSuffix(part, "```\n\n") {
			t.Fatalf("cut outside a paragraph boundary: %q", part[len(part)-20:])
		}
	}
}

func Test短い返答とJSONは分割しない(t *testing.T) {
	for _, text := range []string{"Hello", `[` + strings.Repeat(`"value",`, 400) + `"end"]`} {
		if parts := segments(text); len(parts) != 1 || parts[0] != text {
			t.Fatal(len(parts))
		}
	}
}

func Testユーザーの翻訳は待機中のバックグラウンド翻訳より先に実行される(t *testing.T) {
	s := &Service{gate: make(chan struct{}, 1)}
	ctx := context.Background()
	s.gate <- struct{}{} // a long report is being translated
	order := make(chan string, 2)
	go func() {
		if s.acquire(ctx, false) == nil {
			order <- "background"
			<-s.gate
		}
	}()
	time.Sleep(50 * time.Millisecond) // background waits first
	go func() {
		if s.acquire(ctx, true) == nil {
			order <- "user"
			<-s.gate
		}
	}()
	for s.urgent.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	<-s.gate
	if first := <-order; first != "user" {
		t.Fatal("background ran before the waiting user request")
	}
	if second := <-order; second != "background" {
		t.Fatal(second)
	}
}

func Test長文は分割してワーカーに送り結果を連結する(t *testing.T) {
	if os.Getenv("HERDR_SEGMENT_HELPER") == "1" {
		sc := bufio.NewScanner(os.Stdin)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			var req map[string]string
			_ = json.Unmarshal(sc.Bytes(), &req)
			_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"text": "[" + req["text"] + "]"})
		}
		os.Exit(0)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(ctx, t.TempDir())
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^Test長文は分割してワーカーに送り結果を連結する$")
	cmd.Env = append(os.Environ(), "HERDR_SEGMENT_HELPER=1")
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	s.mu.Lock()
	s.worker = &worker{cmd: cmd, in: in, out: bufio.NewReader(out)}
	s.status.State = "ready"
	s.mu.Unlock()
	text := longReply()
	want := ""
	for _, part := range segments(text) {
		want += "[" + part + "]"
	}
	got, err := s.Translate(ctx, text, "ja")
	if err != nil || got != want {
		t.Fatal(err)
	}
	s.mu.Lock()
	cachedWhole, hit := s.cacheGetLocked(translationKey(s.status.Model, "ja", text))
	s.mu.Unlock()
	if !hit || cachedWhole != want {
		t.Fatal("whole translation not cached")
	}
}

func Test失敗が続くと再試行までの間隔が延びる(t *testing.T) {
	var entry cached
	previous := time.Duration(0)
	for range 9 {
		entry = failedEntry(entry)
		wait := time.Until(entry.retry)
		if wait < previous-time.Second || wait > 33*time.Minute {
			t.Fatal(wait)
		}
		previous = wait
	}
	if previous < 30*time.Minute {
		t.Fatal(previous)
	}
}
