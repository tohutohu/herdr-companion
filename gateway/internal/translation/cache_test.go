package translation

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

func Test翻訳ライフサイクル補助プロセス(t *testing.T) {
	if os.Getenv("HERDR_TRANSLATION_LIFECYCLE_HELPER") != "1" {
		return
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]bool{"ready": true})
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var req map[string]string
		_ = json.Unmarshal(sc.Bytes(), &req)
		f, _ := os.OpenFile(os.Getenv("HERDR_TRANSLATION_CALLS"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		_, _ = f.WriteString("x")
		_ = f.Close()
		if req["text"] == "slow private text" {
			time.Sleep(200 * time.Millisecond)
		}
		if req["text"] == "fail" {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"error": "private error content"})
		} else {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"text": req["target"] + ":" + req["text"]})
		}
	}
	os.Exit(0)
}

func lifecycleService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	t.Setenv("HERDR_TRANSLATION_LIFECYCLE_HELPER", "1")
	t.Setenv("HERDR_TRANSLATION_CALLS", calls)
	for _, sub := range []string{"venv/bin", "models/0.8b"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0700); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(os.Args[0], "'", "'\\''") + "' -test.run='^Test翻訳ライフサイクル補助プロセス$'\n"
	for path, text := range map[string]string{
		"uv": "unused", "runtime-v1": "ok", "models/0.8b/.herdr-ready": revisions["0.8b"], "venv/bin/python": script,
	} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(text), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := New(ctx, dir)
	s.status.Supported = true
	t.Cleanup(func() { s.Close(); cancel() })
	return s, calls
}

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatal("condition timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func Test切断した翻訳を完了して重複推論せず再起動後も再利用する(t *testing.T) {
	s, calls := lifecycleService(t)
	if err := s.Start("0.8b"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { _, err := s.Translate(ctx, "slow private text", "ja"); finished <- err }()
	waitFor(t, func() bool { _, err := os.Stat(calls); return err == nil })
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	got, err := s.Translate(context.Background(), "slow private text", "ja")
	if err != nil || got != "ja:slow private text" {
		t.Fatal(got, err)
	}
	if data, _ := os.ReadFile(calls); string(data) != "x" {
		t.Fatalf("duplicate inference: %q", data)
	}
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	restored := New(s.ctx, s.dir)
	defer restored.Close()
	if got, err := restored.Translate(context.Background(), "slow private text", "ja"); err != nil || got != "ja:slow private text" {
		t.Fatal(got, err)
	}
	if _, err := restored.Translate(context.Background(), "slow private text", "en"); err == nil {
		t.Fatal("directions shared a cache entry or explicit Stop was ignored")
	}
	restored.mu.Lock()
	restored.status.Model = "2b"
	restored.mu.Unlock()
	if _, err := restored.Translate(context.Background(), "slow private text", "ja"); err == nil {
		t.Fatal("models shared a cache entry")
	}
	data, err := os.ReadFile(filepath.Join(s.dir, "timings.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private") || strings.Contains(string(data), "slow") {
		t.Fatal("content leaked into timing log")
	}
	var inference, canceled, hit bool
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var v timing
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatal(err)
		}
		inference = inference || v.Event == "inference" && v.InferenceMS >= 200 && v.Model == "0.8b" && v.Outcome == "ok"
		canceled = canceled || v.Event == "request" && v.Outcome == "canceled"
		hit = hit || v.Event == "request" && v.CacheHit && v.Outcome == "ok"
	}
	if !inference || !canceled || !hit {
		t.Fatalf("incomplete timings: %s", data)
	}
	if stat, _ := os.Stat(filepath.Join(s.dir, "cache.json")); stat.Mode().Perm() != 0600 {
		t.Fatal("cache is not private")
	}
}

func Test異常終了後に次の翻訳で自動復旧し失敗はキャッシュしない(t *testing.T) {
	s, calls := lifecycleService(t)
	if err := s.Start("0.8b"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Translate(context.Background(), "first", "ja"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	w := s.worker
	s.mu.Unlock()
	_ = w.cmd.Cancel()
	waitFor(t, func() bool { return s.Status().State == "failed" })
	if got, err := s.Translate(context.Background(), "second", "en"); err != nil || got != "en:second" {
		t.Fatal(got, err)
	}
	for range 2 {
		if _, err := s.Translate(context.Background(), "fail", "ja"); err == nil {
			t.Fatal("worker error was ignored")
		}
	}
	if data, _ := os.ReadFile(calls); string(data) != "xxxx" {
		t.Fatalf("errors were cached: %q", data)
	}
	data, _ := os.ReadFile(filepath.Join(s.dir, "timings.jsonl"))
	if strings.Contains(string(data), "private error content") {
		t.Fatal("worker error content leaked")
	}
}

func Testキャッシュは最近使った結果を残して容量を制限する(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(ctx, t.TempDir())
	for i := range cacheEntries {
		key := cacheKey("test", string(rune(i)))
		s.cache[key] = cached{Text: "value", Used: uint64(i + 1)}
		s.cacheBytes += len("value")
	}
	s.cacheClock = cacheEntries
	keep := cacheKey("test", string(rune(0)))
	_, _ = s.cacheGetLocked(keep)
	s.cachePutLocked(cacheKey("new", "value"), "new")
	if len(s.cache) != cacheEntries || s.cache[keep].Text != "value" || s.cache[cacheKey("test", string(rune(1)))].Text != "" {
		t.Fatal("LRU eviction failed")
	}
	s.cachePutLocked(cacheKey("too", "large"), strings.Repeat("x", cacheLimit+1))
	if s.cacheBytes > cacheLimit || len(s.cache) > cacheEntries {
		t.Fatal("cache exceeded bounds")
	}
}

func Testセッション単位の表示翻訳は全体設定が無効でも実行され停止後もキャッシュを表示する(t *testing.T) {
	s, _ := lifecycleService(t)
	if err := s.Start("0.8b"); err != nil {
		t.Fatal(err)
	}
	on := true
	raw := []model.Message{{Role: model.RoleAssistant, Blocks: []model.Block{model.ReportBlock("Session report")}}}
	waitFor(t, func() bool {
		return s.Messages("session", raw, true, &on)[0].Blocks[0].Text == "ja:Session report"
	})
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if got := s.Messages("session", raw, false, &on)[0].Blocks[0]; got.Text != "ja:Session report" || got.OriginalText != "Session report" {
		t.Fatal(got)
	}
	if got := s.Messages("session", raw, true)[0].Blocks[0]; got.Text != "Session report" {
		t.Fatal("global off was ignored", got)
	}
}

func Test壊れたキャッシュを無視しタイミングログをローテーションする(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cache.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(ctx, dir)
	if len(s.cache) != 0 {
		t.Fatal("broken cache loaded")
	}
	path := filepath.Join(dir, "timings.jsonl")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 5<<20)), 0600); err != nil {
		t.Fatal(err)
	}
	s.recordTiming(timing{Event: "request", Model: "8b", Outcome: "ok"})
	if stat, err := os.Stat(filepath.Join(dir, "timings.1.jsonl")); err != nil || stat.Size() != 5<<20 {
		t.Fatal(stat, err)
	}
	data, err := os.ReadFile(path)
	var v timing
	if err != nil || json.Unmarshal(data, &v) != nil || v.Model != "8b" || v.Revision != revisions["8b"] {
		t.Fatal(string(data), err)
	}
	if stat, _ := os.Stat(path); stat.Mode().Perm() != 0600 {
		t.Fatal("timing log is not private")
	}
}
