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

// fixtureLines returns every transcript fixture's lines, with "/work/app"
// pointing at the test workspace so file references resolve.
func fixtureLines(t *testing.T) (map[string][][]byte, string) {
	t.Helper()
	ws, err := filepath.Abs(filepath.Join(testdata, "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(testdata, "*.jsonl"))
	if err != nil || len(paths) == 0 {
		t.Fatal("no fixtures", err)
	}
	out := map[string][][]byte{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		raw = bytes.ReplaceAll(raw, []byte("/work/app"), []byte(ws))
		out[filepath.Base(path)] = bytes.SplitAfter(raw, []byte("\n"))
	}
	return out, ws
}

func decodeFixture(lines [][]byte, sink deadletter.Sink) *Transcript {
	tr := newTranscript()
	for _, l := range lines {
		tr.decodeLine(l, gatewayID(cacheSession), sink)
	}
	return tr
}

func Test差分変換は何行ずつ追記しても毎回すべて変換し直した結果と一致する(t *testing.T) {
	fixtures, ws := fixtureLines(t)
	working := &providers.Live{HerdrStatus: herdr.StatusWorking, Cwd: ws}
	// The cache never converts a blocked session incrementally, but blocked
	// is where a call's pending state shows, so it checks that dependency.
	blocked := &providers.Live{HerdrStatus: herdr.StatusBlocked, Cwd: ws}
	for name, lines := range fixtures {
		for _, root := range []string{"", ws} {
			for _, live := range []*providers.Live{nil, working, blocked} {
				for _, step := range []int{1, 2, 3, 5, 7} {
					opt := ParseOptions{SessionID: gatewayID(cacheSession), Root: root, Live: live}
					tr, conv := newTranscript(), new(conversion)
					for n := 0; n < len(lines); {
						next := min(n+step, len(lines))
						for _, l := range lines[n:next] {
							tr.decodeLine(l, gatewayID(cacheSession), deadletter.Nop{})
						}
						n = next
						got := conv.update(tr, opt)
						if want := decodeFixture(lines[:n], deadletter.Nop{}).Messages(opt); !reflect.DeepEqual(got, want) {
							t.Fatalf("%s root=%q live=%v step=%d n=%d\ngot=%+v\nwant=%+v", name, root, live != nil, step, n, got, want)
						}
						// Nothing appended: the same messages again.
						if again := conv.update(tr, opt); !reflect.DeepEqual(again, got) {
							t.Fatalf("%s n=%d: 追記なしの再変換が変わった", name, n)
						}
					}
				}
			}
		}
	}
}

func Test結果が先に書かれた呼び出しはツール名がわかった時点で変換し直す(t *testing.T) {
	result := `{"type":"user","uuid":"u1","timestamp":"2026-10-01T00:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call1","content":"\"Color\"=\"Red\""}]}}`
	call := `{"type":"assistant","uuid":"a1","timestamp":"2026-10-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call1","name":"AskUserQuestion","input":{"questions":[{"question":"Which color?","header":"Color","options":[{"label":"Red"}]}]}}]}}`
	opt := ParseOptions{SessionID: gatewayID(cacheSession)}
	tr, conv := newTranscript(), new(conversion)
	tr.decodeLine([]byte(result), opt.SessionID, deadletter.Nop{})
	if got := conv.update(tr, opt); len(got) != 1 || got[0].Role != model.RoleTool {
		t.Fatalf("名前がわかる前は通常の結果として出る: %+v", got)
	}
	tr.decodeLine([]byte(call), opt.SessionID, deadletter.Nop{})
	got := conv.update(tr, opt)
	if want := tr.Messages(opt); !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%+v\nwant=%+v", got, want)
	}
	if len(got) != 1 || got[0].ID != "a1" {
		t.Fatalf("質問への回答は質問カードに含まれ、結果としては出ない: %+v", got)
	}
}

// assertIncremental appends lines one at a time and compares each update
// with converting everything again.
func assertIncremental(t *testing.T, opt ParseOptions, lines ...string) {
	t.Helper()
	tr, conv := newTranscript(), new(conversion)
	for n, l := range lines {
		tr.decodeLine([]byte(l), opt.SessionID, deadletter.Nop{})
		got := conv.update(tr, opt)
		want := decodeFixture(func() (out [][]byte) {
			for _, l := range lines[:n+1] {
				out = append(out, []byte(l))
			}
			return out
		}(), deadletter.Nop{}).Messages(opt)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("n=%d\ngot=%+v\nwant=%+v", n+1, got, want)
		}
	}
}

func Test新しい指示で放棄された呼び出しは結果がなくても承認待ちでなくなる(t *testing.T) {
	blocked := &providers.Live{HerdrStatus: herdr.StatusBlocked}
	assertIncremental(t, ParseOptions{SessionID: gatewayID(cacheSession), Live: blocked},
		`{"type":"user","uuid":"u1","timestamp":"2026-10-01T00:00:01Z","message":{"role":"user","content":"消して"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-10-01T00:00:02Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call1","name":"Bash","input":{"command":"rm -rf build"}}]}}`,
		`{"type":"user","uuid":"u2","timestamp":"2026-10-01T00:00:03Z","message":{"role":"user","content":"やっぱりやめて"}}`,
	)
}

func Test放棄されたあとに届いた結果で質問は回答済みになる(t *testing.T) {
	assertIncremental(t, ParseOptions{SessionID: gatewayID(cacheSession)},
		`{"type":"assistant","uuid":"a1","timestamp":"2026-10-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"call1","name":"AskUserQuestion","input":{"questions":[{"question":"Which color?","header":"Color","options":[{"label":"Red"}]}]}}]}}`,
		`{"type":"user","uuid":"u1","timestamp":"2026-10-01T00:00:02Z","message":{"role":"user","content":"別の話"}}`,
		`{"type":"user","uuid":"u2","timestamp":"2026-10-01T00:00:03Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call1","content":"\"Which color?\"=\"Red\""}]}}`,
	)
}

func Test差分変換でもdead_letterを落とさない(t *testing.T) {
	fixtures, ws := fixtureLines(t)
	key := func(e deadletter.Entry) string { return string(e.Kind) + "\x00" + e.Error + "\x00" + string(e.Raw) }
	for name, lines := range fixtures {
		opt := ParseOptions{SessionID: gatewayID(cacheSession), Root: ws}
		full := &deadletter.Recorder{}
		decodeFixture(lines, deadletter.Nop{}).Messages(ParseOptions{SessionID: opt.SessionID, Root: ws, Sink: full})
		inc := &deadletter.Recorder{}
		opt.Sink = inc
		tr, conv := newTranscript(), new(conversion)
		for _, l := range lines {
			tr.decodeLine(l, gatewayID(cacheSession), deadletter.Nop{})
			conv.update(tr, opt)
		}
		recorded := map[string]bool{}
		for _, e := range inc.Entries {
			recorded[key(e)] = true
		}
		for _, e := range full.Entries {
			if !recorded[key(e)] {
				t.Errorf("%s: 差分変換で記録されなかった: %s %s", name, e.Kind, e.Error)
			}
		}
	}
}

func Testキャッシュしたメッセージは追記のたびに従来の解析結果と一致する(t *testing.T) {
	fixtures, ws := fixtureLines(t)
	for _, raw := range fixtures {
		p, path, write := cacheProvider(t)
		lines := raw
		for n := 1; n <= len(lines); n++ {
			write(bytes.Join(lines[:n], nil))
			for _, live := range []*providers.Live{nil, {HerdrStatus: herdr.StatusWorking, Cwd: "/work"}, {HerdrStatus: herdr.StatusWorking, Cwd: ws}} {
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
	fixtures, _ := fixtureLines(t)
	for _, lines := range fixtures {
		summariesMatch(t, lines)
	}
}

func summariesMatch(t *testing.T, lines [][]byte) {
	t.Helper()
	p, path, write := cacheProvider(t)
	live := &providers.Live{HerdrStatus: herdr.StatusWorking}
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

func Test再読込のためにForgetするとトランスクリプトと要約のキャッシュを捨てる(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(testdata, "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	p, path, write := cacheProvider(t)
	write(raw)
	ctx := context.Background()
	if _, err := p.Messages(ctx, cacheSession, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := p.summaryPath(ctx, path, cacheSession, nil); err != nil {
		t.Fatal(err)
	}
	if len(p.transcripts) != 1 || len(p.summaries) != 1 {
		t.Fatalf("transcripts=%d summaries=%d", len(p.transcripts), len(p.summaries))
	}
	p.Forget(cacheSession)
	if len(p.transcripts) != 0 || len(p.summaries) != 0 {
		t.Errorf("Forget 後も残っている: transcripts=%d summaries=%d", len(p.transcripts), len(p.summaries))
	}
	assertCachedMessages(t, p, path, nil)
}

// cachedAfterReadingAnother reads a transcript, then a second one, and
// reports whether the first is still cached under the given budget.
func cachedAfterReadingAnother(t *testing.T, budget func(size int64) int64) bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(testdata, "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first.jsonl"), filepath.Join(dir, "second.jsonl")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := New(dir, nil, deadletter.Nop{})
	c, err := p.transcript(first, cacheSession)
	if err != nil {
		t.Fatal(err)
	}
	kept := c.kept
	c.mu.Unlock()
	p.SetCacheBytes(budget(kept))
	if c, err = p.transcript(second, cacheSession); err != nil {
		t.Fatal(err)
	}
	c.mu.Unlock()
	_, ok := p.transcripts[first]
	return ok
}

func Testキャッシュの上限に収まる間は別のトランスクリプトを読んでも追い出さない(t *testing.T) {
	if !cachedAfterReadingAnother(t, func(size int64) int64 { return 2 * size }) {
		t.Error("上限内なのに最初のトランスクリプトが追い出された")
	}
}

func Testキャッシュの上限を超えると最も古いトランスクリプトを追い出す(t *testing.T) {
	if cachedAfterReadingAnother(t, func(size int64) int64 { return 2*size - 1 }) {
		t.Error("上限を超えても最初のトランスクリプトが残っている")
	}
}
