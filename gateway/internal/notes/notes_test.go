package notes

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

func Testメモをファイルに保存して読み戻せる(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "notes.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Create("claude:a", Change{Text: ptr("PRのレビューに対応させて"), Author: "codex:w"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	b, _ := s.Create("claude:a", Change{Text: ptr("マージされたらアーカイブしてよい")})
	s.Create("codex:b", Change{Text: ptr("ずっと待機")})

	re, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := re.For("claude:a")
	if len(got) != 2 || got[0].ID != a.ID || got[1].ID != b.ID || got[0].Author != "codex:w" {
		t.Fatalf("notes = %+v", got)
	}
	if len(re.All()) != 3 {
		t.Errorf("all = %+v", re.All())
	}
}

func Test期限を過ぎたメモは消える(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "notes.json"))
	now := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	n, err := s.Create("claude:a", Change{Text: ptr("今日だけ"), ExpiresAt: ptr(now.Add(time.Hour))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("claude:a", Change{Text: ptr("x"), ExpiresAt: ptr(now.Add(-time.Minute))}); err == nil {
		t.Error("a note that has already expired was accepted")
	}
	now = now.Add(2 * time.Hour)
	if got := s.For("claude:a"); len(got) != 0 {
		t.Fatalf("expired notes are still listed: %+v", got)
	}
	if _, err := s.Get(n.ID); err != ErrNotFound {
		t.Errorf("get expired = %v", err)
	}
}

func Test長すぎるメモと多すぎるメモは拒否する(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "notes.json"))
	if _, err := s.Create("claude:a", Change{Text: ptr(strings.Repeat("あ", MaxText))}); err == nil {
		t.Error("an over-long note was accepted")
	}
	for i := 0; i < MaxPerSession; i++ {
		if _, err := s.Create("claude:a", Change{Text: ptr("x")}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Create("claude:a", Change{Text: ptr("x")}); err == nil {
		t.Error("note over the per-session limit was accepted")
	}
	if _, err := s.Create("claude:b", Change{Text: ptr("x")}); err != nil {
		t.Errorf("another session is limited too: %v", err)
	}
}
