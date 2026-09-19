package claude

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

func Test要約キャッシュは状態別に従来の解析結果と一致する(t *testing.T) {
	p := New(t.TempDir(), nil, deadletter.Nop{})
	for _, fixture := range []string{"normal.jsonl", "pending_approval.jsonl", "queued_message.jsonl", "compaction.jsonl"} {
		path := filepath.Join(testdata, fixture)
		for _, live := range []*providers.Live{nil, {HerdrStatus: herdr.StatusWorking}, {HerdrStatus: herdr.StatusBlocked}} {
			tr, err := p.loadPath(path, "test-session")
			if err != nil {
				t.Fatal(err)
			}
			want := tr.summary(ParseOptions{SessionID: gatewayID("test-session"), Live: live})
			want.NativeID = "test-session"
			for i := 0; i < 2; i++ {
				got, err := p.summaryPath(context.Background(), path, "test-session", live)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("%s live=%v got=%+v want=%+v err=%v", fixture, live, got, want, err)
				}
			}
		}
	}
}

func Test要約キャッシュは追記と切り詰めと置換を反映する(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	raw, err := os.ReadFile(filepath.Join(testdata, "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	write := func(b []byte) {
		t.Helper()
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(raw)
	p := New(dir, nil, deadletter.Nop{})
	check := func() {
		t.Helper()
		got, err := p.summaryPath(context.Background(), path, "test-session", nil)
		if err != nil {
			t.Fatal(err)
		}
		tr, err := p.loadPath(path, "test-session")
		if err != nil {
			t.Fatal(err)
		}
		want := tr.summary(ParseOptions{SessionID: gatewayID("test-session")})
		want.NativeID = "test-session"
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got=%+v want=%+v", got, want)
		}
	}
	check()
	write(append(raw, []byte("\n{\"type\":\"custom-title\",\"customTitle\":\"changed\"}\n")...))
	check()
	write(nil)
	check()
	replacement := filepath.Join(dir, "replacement")
	if err := os.WriteFile(replacement, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	check()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := p.summaryPath(context.Background(), path, "test-session", nil); !os.IsNotExist(err) {
		t.Fatalf("deleted file: %v", err)
	}
}

func Test除外された履歴は解析しない(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "projects", "test")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "excluded-session.jsonl"), []byte("invalid JSON\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rec := &deadletter.Recorder{}
	p := New(dir, nil, rec)
	got, err := p.RecentExcluding(context.Background(), time.Now().Add(-time.Hour), map[string]bool{"excluded-session": true})
	if err != nil || len(got) != 0 || len(rec.Entries) != 0 {
		t.Fatalf("got=%v err=%v deadletters=%d", got, err, len(rec.Entries))
	}
}

func Test要約キャッシュは並行アクセスと上限に対応する(t *testing.T) {
	p := New(t.TempDir(), nil, deadletter.Nop{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				if _, err := p.summaryPath(context.Background(), filepath.Join(testdata, "normal.jsonl"), "test-session", nil); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	for i := 0; i < maxCachedSummaries+1; i++ {
		path := filepath.Join(p.configDir, fmt.Sprintf("%d.jsonl", i))
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := p.summaryPath(context.Background(), path, "test-session", nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(p.summaries) != maxCachedSummaries {
		t.Fatalf("cache size=%d", len(p.summaries))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.summaryPath(ctx, filepath.Join(testdata, "normal.jsonl"), "test-session", nil); err != context.Canceled {
		t.Fatalf("canceled: %v", err)
	}
}

func Test未更新の要約は再解析せず返却値から変更されない(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	raw, err := os.ReadFile(filepath.Join(testdata, "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\ninvalid JSON\n")...)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	rec := &deadletter.Recorder{}
	p := New(dir, nil, rec)
	first, err := p.summaryPath(context.Background(), path, "test-session", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := copySummary(first)
	if first.Context != nil {
		first.Context.UsedTokens = -1
	}
	if first.Cost != nil {
		first.Cost.USD = -1
	}
	count := len(rec.Entries)
	if count == 0 {
		t.Fatal("fixture must record a parse error")
	}
	again, err := p.summaryPath(context.Background(), path, "test-session", nil)
	if err != nil || !reflect.DeepEqual(again, want) || len(rec.Entries) != count {
		t.Fatalf("cache not reused or mutated: err=%v records=%d want=%d", err, len(rec.Entries), count)
	}
}
