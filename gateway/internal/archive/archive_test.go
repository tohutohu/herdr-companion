package archive

import (
	"path/filepath"
	"testing"
	"time"
)

func Testアーカイブ状態をファイルに保存して読み戻せる(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "archive.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Has("claude:a") || len(s.IDs()) != 0 {
		t.Fatal("new store must be empty")
	}
	if err := s.Add("claude:a"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	s.Add("codex:b")
	s.Add("claude:a") // 二重に追加しても日時は変わらない

	re, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if ids := re.IDs(); len(ids) != 2 || ids[0] != "codex:b" || ids[1] != "claude:a" {
		t.Errorf("ids = %v", ids)
	}
	if err := re.Remove("codex:b"); err != nil {
		t.Fatal(err)
	}
	re.Remove("missing")
	again, _ := Open(path)
	if again.Has("codex:b") || !again.Has("claude:a") {
		t.Errorf("after remove = %v", again.IDs())
	}
}

func Test複数のアーカイブを一度に保存できる(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "archive.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMany([]string{"claude:a", "codex:b"}); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Has("claude:a") || !reloaded.Has("codex:b") {
		t.Fatalf("ids = %v", reloaded.IDs())
	}
	if err := reloaded.AddMany([]string{"claude:a", "opencode:c"}); err != nil {
		t.Fatal(err)
	}
	if ids := reloaded.IDs(); len(ids) != 3 {
		t.Fatalf("ids after second batch = %v", ids)
	}
}
