package claude

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

const cacheSession = "test-session"

// cacheProvider returns a provider whose only transcript is cacheSession's,
// and a function that writes that transcript.
func cacheProvider(t *testing.T) (*Provider, string, func([]byte)) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "projects", "p", cacheSession+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	write := func(b []byte) {
		t.Helper()
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return New(dir, nil, deadletter.Nop{}), path, write
}

// uncachedMessages is what Messages returned before the cache.
func uncachedMessages(t *testing.T, p *Provider, path string, live *providers.Live) []model.Message {
	t.Helper()
	tr, err := p.loadPath(path, cacheSession)
	if err != nil {
		t.Fatal(err)
	}
	return tr.Messages(ParseOptions{SessionID: gatewayID(cacheSession), Root: root(tr, live), Live: live, Sink: deadletter.Nop{}})
}

func assertCachedMessages(t *testing.T, p *Provider, path string, live *providers.Live) {
	t.Helper()
	for i := 0; i < 2; i++ {
		got, err := p.Messages(context.Background(), cacheSession, live)
		if err != nil {
			t.Fatal(err)
		}
		if want := uncachedMessages(t, p, path, live); !reflect.DeepEqual(got, want) {
			t.Fatalf("live=%v\ngot=%+v\nwant=%+v", live, got, want)
		}
	}
}

func Testキャッシュしたメッセージは追記のたびに従来の解析結果と一致する(t *testing.T) {
	for _, fixture := range []string{"normal.jsonl", "pending_approval.jsonl", "queued_message.jsonl", "compaction.jsonl", "ask_user_question.jsonl"} {
		raw, err := os.ReadFile(filepath.Join(testdata, fixture))
		if err != nil {
			t.Fatal(err)
		}
		p, path, write := cacheProvider(t)
		lines := bytes.SplitAfter(raw, []byte("\n"))
		for n := 1; n <= len(lines); n++ {
			write(bytes.Join(lines[:n], nil))
			for _, live := range []*providers.Live{nil, {HerdrStatus: herdr.StatusWorking, Cwd: "/work"}} {
				assertCachedMessages(t, p, path, live)
			}
		}
	}
}

func Test書きかけの行は書き終わるまで読まない(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(testdata, "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	p, path, write := cacheProvider(t)
	half := bytes.LastIndexByte(raw[:len(raw)-1], '\n') + 20
	write(raw[:half])
	got, err := p.Messages(context.Background(), cacheSession, nil)
	if err != nil {
		t.Fatal(err)
	}
	write(raw[:bytes.LastIndexByte(raw[:len(raw)-1], '\n')+1])
	if want := uncachedMessages(t, p, path, nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("書きかけの行が読まれた\ngot=%+v\nwant=%+v", got, want)
	}
	// 改行がなくても JSON として完結した最後の行は読む。
	write(bytes.TrimRight(raw, "\n"))
	assertCachedMessages(t, p, path, nil)
	write(raw)
	assertCachedMessages(t, p, path, nil)
}

func Test切り詰めや置き換えられたトランスクリプトは最初から読み直す(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(testdata, "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := os.ReadFile(filepath.Join(testdata, "compaction.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	p, path, write := cacheProvider(t)
	write(raw)
	assertCachedMessages(t, p, path, nil)
	write(raw[:bytes.IndexByte(raw, '\n')+1])
	assertCachedMessages(t, p, path, nil)
	replacement := path + ".new"
	if err := os.WriteFile(replacement, append(other, raw...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	assertCachedMessages(t, p, path, nil)
}

func Test画像データはキャッシュに残さず画像は元のファイルから返す(t *testing.T) {
	img := bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, imageDataLine)
	data := base64.StdEncoding.EncodeToString(img)
	line, err := json.Marshal(map[string]any{
		"type": "user", "uuid": "u-img", "timestamp": "2026-09-17T00:00:00Z",
		"message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "t1", "content": []any{
				map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": data}},
			}},
		}},
		"toolUseResult": map[string]any{"type": "image", "file": map[string]any{"base64": data, "type": "image/png"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tool := `{"type":"assistant","uuid":"a1","timestamp":"2026-09-17T00:00:00Z","message":{"id":"m1","role":"assistant","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/tmp/a.png"}}]}}`
	p, path, write := cacheProvider(t)
	write([]byte(tool + "\n" + string(line) + "\n"))
	assertCachedMessages(t, p, path, nil)

	c, err := p.transcript(path, cacheSession)
	if err != nil {
		t.Fatal(err)
	}
	kept := c.kept
	c.mu.Unlock()
	if kept >= int64(len(data)) {
		t.Fatalf("画像データが残っている: kept=%d", kept)
	}
	msgs, err := p.Messages(context.Background(), cacheSession, nil)
	if err != nil {
		t.Fatal(err)
	}
	var url string
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == model.BlockImage {
				url = b.URL
			}
		}
	}
	if url == "" {
		t.Fatalf("画像ブロックがない: %+v", msgs)
	}
	parts := strings.Split(url, "/")
	mime, got, err := p.Image(context.Background(), cacheSession, parts[len(parts)-3], 0)
	if err != nil || mime != "image/png" || !bytes.Equal(got, img) {
		t.Fatalf("mime=%q len=%d err=%v", mime, len(got), err)
	}
}

func Test実行中セッションの要約は追記分だけ読み直しても従来と一致する(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(testdata, "queued_message.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	p, path, write := cacheProvider(t)
	live := &providers.Live{HerdrStatus: herdr.StatusWorking}
	lines := bytes.SplitAfter(raw, []byte("\n"))
	for n := 1; n <= len(lines); n++ {
		write(bytes.Join(lines[:n], nil))
		got, err := p.summaryPath(context.Background(), path, cacheSession, live)
		if err != nil {
			t.Fatal(err)
		}
		tr, err := p.loadPath(path, cacheSession)
		if err != nil {
			t.Fatal(err)
		}
		want := tr.summary(ParseOptions{SessionID: gatewayID(cacheSession), Live: live})
		want.NativeID = cacheSession
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("n=%d\ngot=%+v\nwant=%+v", n, got, want)
		}
	}
}
