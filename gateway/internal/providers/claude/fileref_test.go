package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// fileRefWorkspace makes a repository with a subdirectory the session is now
// in, like an agent that started in repo/ and later cd'd into repo/sub/.
//
//	repo/out/video.mp4
//	repo/sub/promo.js
func fileRefWorkspace(t *testing.T) (repo, sub string) {
	t.Helper()
	// t.TempDir would put the Japanese test name into the path, which the
	// file-reference pattern does not treat as part of a path.
	repo, err := os.MkdirTemp("", "fileref")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(repo) })
	sub = filepath.Join(repo, "sub")
	for _, p := range []string{filepath.Join(repo, "out", "video.mp4"), filepath.Join(sub, "promo.js")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return repo, sub
}

// replyLine is an assistant transcript line written in cwd.
func replyLine(t *testing.T, uuid, cwd, text string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"type": "assistant", "uuid": uuid, "timestamp": "2026-09-23T00:00:00Z", "cwd": cwd,
		"message": map[string]any{"role": "assistant", "content": []map[string]string{{"type": "text", "text": text}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fileBlocks(msgs []model.Message) []model.Block {
	var out []model.Block
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == model.BlockFile {
				out = append(out, b)
			}
		}
	}
	return out
}

// 前提: 返答はリポジトリ直下（repo/）で書かれ、その後セッションは repo/sub/ に移っている。
// 検証: 返答中の相対パスは書いた時点の cwd から探され、今のルートの外なので絶対パスで返る。
// 表示名は書かれたとおりの短いパスのまま。
func Test以前のcwdで書いた相対パスもそのときのcwdを基準にファイルとして検出される(t *testing.T) {
	repo, sub := fileRefWorkspace(t)
	tr := decodeLines(t, []string{replyLine(t, "a1", repo, "動画は out/video.mp4 です")})

	got := fileBlocks(tr.Messages(ParseOptions{SessionID: "claude:test", Root: sub}))

	want := []model.Block{{Type: model.BlockFile, Path: filepath.Join(repo, "out", "video.mp4"), Text: "out/video.mp4", Size: 1}}
	if !slices.Equal(got, want) {
		t.Fatalf("file blocks = %+v, want %+v", got, want)
	}
}

// 前提: 返答は repo/ で書かれ、参照先は今のルート repo/sub/ の中にある。
// 検証: ファイル API が今のルートから解決できるよう、今のルートからの相対パスで返る。
func Test以前のcwdで書いたパスが今のルートの中なら今のルートからの相対パスになる(t *testing.T) {
	repo, sub := fileRefWorkspace(t)
	tr := decodeLines(t, []string{replyLine(t, "a1", repo, "sub/promo.js を直しました")})

	got := fileBlocks(tr.Messages(ParseOptions{SessionID: "claude:test", Root: sub}))

	want := []model.Block{{Type: model.BlockFile, Path: "promo.js", Size: 1}}
	if !slices.Equal(got, want) {
		t.Fatalf("file blocks = %+v, want %+v", got, want)
	}
}

// 前提: 返答を書いたディレクトリはもう無い（移動・削除された）。
// 検証: 今のルートを基準に探す（移動前と同じ中身なら、そのまま見つかる）。
func Test記録されたcwdが無くなっていれば今のルートを基準に探す(t *testing.T) {
	_, sub := fileRefWorkspace(t)
	gone := filepath.Join(t.TempDir(), "moved-away")
	tr := decodeLines(t, []string{replyLine(t, "a1", gone, "promo.js を直しました")})

	got := fileBlocks(tr.Messages(ParseOptions{SessionID: "claude:test", Root: sub}))

	want := []model.Block{{Type: model.BlockFile, Path: "promo.js", Size: 1}}
	if !slices.Equal(got, want) {
		t.Fatalf("file blocks = %+v, want %+v", got, want)
	}
}

// 前提: 返答は repo/sub/ で書かれ、repo/ の中のファイルを絶対パスで挙げている。
// 検証: 書いた時点の cwd の外でも、今のルートの中なら今のルートから見つかる。
func Test絶対パスは今のルートからも探される(t *testing.T) {
	repo, sub := fileRefWorkspace(t)
	video := filepath.Join(repo, "out", "video.mp4")
	tr := decodeLines(t, []string{replyLine(t, "a1", sub, "書き出し先: "+video)})

	got := fileBlocks(tr.Messages(ParseOptions{SessionID: "claude:test", Root: repo}))

	want := []model.Block{{Type: model.BlockFile, Path: filepath.Join("out", "video.mp4"), Size: 1}}
	if !slices.Equal(got, want) {
		t.Fatalf("file blocks = %+v, want %+v", got, want)
	}
}

// 前提: 要約（Root なし）ではファイル参照を作らない。
// 検証: エントリに cwd が記録されていても、ファイルブロックは作られない。
func Test今のルートが無ければエントリのcwdがあってもファイル参照を作らない(t *testing.T) {
	repo, _ := fileRefWorkspace(t)
	tr := decodeLines(t, []string{replyLine(t, "a1", repo, "out/video.mp4")})

	if got := fileBlocks(tr.Messages(ParseOptions{SessionID: "claude:test"})); len(got) != 0 {
		t.Fatalf("file blocks = %+v, want none", got)
	}
}

// 前提: 一度変換したあとで、参照先のファイルが消える。
// 検証: ポーリングごとに stat し直さないよう、同じ文面の検出結果は使い回される
// （消えたファイルのリンクはファイル API が not found を返す、既存の古い参照と同じ扱い）。
func Test一度調べたファイル参照は再変換で使い回される(t *testing.T) {
	repo, _ := fileRefWorkspace(t)
	tr := decodeLines(t, []string{replyLine(t, "a1", repo, "out/video.mp4")})
	opt := ParseOptions{SessionID: "claude:test", Root: repo}
	first := fileBlocks(tr.Messages(opt))
	if err := os.Remove(filepath.Join(repo, "out", "video.mp4")); err != nil {
		t.Fatal(err)
	}

	second := fileBlocks(tr.Messages(opt))

	if len(first) != 1 || !slices.Equal(first, second) {
		t.Fatalf("first = %+v, second = %+v", first, second)
	}
}

// 前提: セッションが repo/ と repo/sub/ で作業した記録がある。
// 検証: ファイル API が以前の cwd のファイルも開けるよう、作業したディレクトリを重複なく返す。
func Test作業したディレクトリをファイルを開けるルートとして返す(t *testing.T) {
	repo, sub := fileRefWorkspace(t)
	p, _, write := cacheProvider(t)
	write([]byte(strings.Join([]string{
		replyLine(t, "a1", repo, "one"),
		replyLine(t, "a2", sub, "two"),
		replyLine(t, "a3", repo, "three"),
	}, "\n") + "\n"))

	got := p.SessionFileRoots(context.Background(), cacheSession)

	if want := []string{repo, sub}; !slices.Equal(got, want) {
		t.Fatalf("roots = %v, want %v", got, want)
	}
}
