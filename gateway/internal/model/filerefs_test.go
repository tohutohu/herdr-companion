package model

import (
	"os"
	"path/filepath"
	"testing"
)

func Testテキスト中の存在するファイル参照だけをリンクにする(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "src", "auth"), 0o755)
	os.WriteFile(filepath.Join(root, "src", "auth", "middleware.go"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, "README.md"), []byte("x"), 0o644)

	text := "修正しました。src/auth/middleware.go:42 と `README.md` を参照。" +
		"存在しない nope.go や v1.2.3、https://example.com/a.go、../outside.go は無視。" +
		"絶対パス " + filepath.Join(root, "README.md") + " も可。src/auth/middleware.go:42 は重複。"
	got := ExtractFileRefs(text, root)
	want := []Block{
		{Type: BlockFile, Path: "src/auth/middleware.go", Line: 42},
		{Type: BlockFile, Path: "README.md"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestTruncateは長い文字列を切り詰める(t *testing.T) {
	if got := Truncate("あいうえお", 3); got != "あいう\n… (truncated)" {
		t.Errorf("got %q", got)
	}
	if got := Truncate("abc", 3); got != "abc" {
		t.Errorf("got %q", got)
	}
}
