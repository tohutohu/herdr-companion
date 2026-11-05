package dsh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/resident"
)

const residentTestResult = `{"schemaVersion":1,"outcome":"unchanged","summary":"確認済み","report":"異常なし","notification":{"requested":false,"key":null},"statePatch":[],"memorySummary":null,"nextWakeAt":null,"question":null,"artifacts":[]}`

func TestDSH実ランタイムの常駐結果と質問と読取専用(t *testing.T) {
	binary := os.Getenv("HERDR_DSH_E2E_BINARY")
	if binary == "" {
		t.Skip("set HERDR_DSH_E2E_BINARY")
	}
	for _, kind := range []string{"result", "question", "write"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/messages" {
					http.Error(w, "unexpected path", 404)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				emit := func(name string, v map[string]any) {
					v["type"] = name
					b, _ := json.Marshal(v)
					fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
				}
				emit("message_start", map[string]any{"message": map[string]any{"id": "msg-test", "type": "message", "role": "assistant", "model": DefaultModel, "content": []any{}, "usage": map[string]int{"input_tokens": 10, "output_tokens": 0}}})
				stop := "end_turn"
				if calls.Add(1) == 1 && kind != "result" {
					stop = "tool_use"
					tool, input := "ask_user_question", `{"questions":[{"id":"choice","question":"Continue?","options":[{"label":"Yes"},{"label":"No"}]}]}`
					if kind == "write" {
						tool, input = "write", `{"file_path":"must-not-exist.txt","content":"unauthorized"}`
					}
					emit("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "tool_use", "id": "tool-1", "name": tool, "input": map[string]any{}}})
					emit("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": input}})
				} else {
					emit("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
					emit("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "text_delta", "text": residentTestResult}})
				}
				emit("content_block_stop", map[string]any{"index": 0})
				emit("message_delta", map[string]any{"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 10}})
				emit("message_stop", map[string]any{})
			}))
			defer server.Close()
			t.Setenv("DEEPSEEK_BASE_URL", server.URL)
			p, err := New(Config{Binary: binary, APIKey: "test-local-only"}, t.TempDir(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			req := residentRequestForTest(t, "Check the workspace")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			out, err := p.ResidentRunner().Run(ctx, req, resident.RunHooks{})
			if err != nil {
				t.Fatal(err)
			}
			raw := out.Structured
			if len(raw) == 0 {
				raw = []byte(out.Text)
			}
			result, err := resident.ParseResult(raw)
			if err != nil {
				t.Fatalf("result: %+v: %v", out, err)
			}
			if kind == "question" && (result.Outcome != resident.OutcomeBlocked || result.Question == nil) {
				t.Fatalf("question lost: %+v", out)
			}
			if kind == "write" {
				if _, err = os.Stat(filepath.Join(req.Cwd, "must-not-exist.txt")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("read-only write was allowed")
				}
				if len(out.PermissionDenials) == 0 {
					t.Fatal("sandbox denial missing from run result")
				}
			}
		})
	}
}

func residentRequestForTest(t *testing.T, prompt string) resident.RunRequest {
	t.Helper()
	return resident.RunRequest{Cwd: t.TempDir(), Prompt: prompt, Schema: resident.ResultSchema, Permissions: resident.Permissions{Profile: resident.ProfileReadOnly}}
}

func TestDSH常駐は独立して完了し通常会話へ引き継げる(t *testing.T) {
	p, _, _ := testProvider(t)
	ordinary := startTest(t, p, "hold")
	var notified atomic.Int32
	p.SetDirectListener(func(string) { notified.Add(1) })
	var prepared, sent bool
	out, err := p.ResidentRunner().Run(context.Background(), residentRequestForTest(t, "hello"), resident.RunHooks{
		BeforeSend: func(id, model string) error {
			if _, err := p.get(id); !errors.Is(err, providers.ErrNotFound) {
				t.Error("resident registered before completion")
			}
			if model != DefaultModel {
				t.Error(model)
			}
			prepared = true
			return nil
		},
		Sent: func() {
			if !prepared {
				t.Error("sent before intent persisted")
			}
			sent = true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sent || out.Error != "" || out.Usage.InputTokens != 16 || out.Usage.CachedInputTokens != 3 || out.Usage.OutputTokens != 7 || out.Usage.NumTurns != 1 {
		t.Fatalf("output: %+v", out)
	}
	if _, err = resident.ParseResult([]byte(out.Text)); err != nil {
		t.Fatal(err)
	}
	if notified.Load() != 0 {
		t.Fatal("resident notified ordinary runtime")
	}
	waitInfo(t, p, ordinary, func(i providers.DirectInfo) bool { return i.Alive && i.Running })
	waitInfo(t, p, out.NativeID, func(i providers.DirectInfo) bool { return !i.Alive && !i.Running })
	if err = p.ResidentRunner().Rename(context.Background(), out.NativeID, "常駐から続行"); err != nil {
		t.Fatal(err)
	}
	if _, err = p.DirectStart(context.Background(), providers.DirectStart{Resume: out.NativeID}); err != nil {
		t.Fatal(err)
	}
	summary, _ := p.Summary(context.Background(), out.NativeID, nil)
	if summary.Title != "常駐から続行" {
		t.Fatal(summary.Title)
	}
}

func TestDSH常駐の質問は回答待ちで承認は拒否する(t *testing.T) {
	for _, prompt := range []string{"question", "approval"} {
		t.Run(prompt, func(t *testing.T) {
			p, _, _ := testProvider(t)
			out, err := p.ResidentRunner().Run(context.Background(), residentRequestForTest(t, prompt), resident.RunHooks{})
			if err != nil {
				t.Fatal(err)
			}
			if prompt == "question" {
				parsed, err := resident.ParseResult(out.Structured)
				if err != nil {
					t.Fatal(err)
				}
				if parsed.Outcome != resident.OutcomeBlocked || parsed.Question == nil || len(parsed.Question.Options) != 2 {
					t.Fatalf("question: %+v", parsed)
				}
			} else if len(out.PermissionDenials) != 1 {
				t.Fatalf("denials: %+v", out)
			}
			waitInfo(t, p, out.NativeID, func(i providers.DirectInfo) bool { return !i.Alive && !i.Waiting })
		})
	}
}

func TestDSH常駐の停止と未送信時の破棄(t *testing.T) {
	t.Run("失敗後に以前のJSONを成功扱いしない", func(t *testing.T) {
		p, _, _ := testProvider(t)
		out, err := p.ResidentRunner().Run(context.Background(), residentRequestForTest(t, "failed-result"), resident.RunHooks{})
		if err != nil {
			t.Fatal(err)
		}
		if out.Text != "" || out.Error != "model failed" {
			t.Fatalf("failed output: %+v", out)
		}
	})
	t.Run("送信直前の失敗で履歴を公開しない", func(t *testing.T) {
		p, cfg, dir := testProvider(t)
		sentinel := errors.New("store unavailable")
		_, err := p.ResidentRunner().Run(context.Background(), residentRequestForTest(t, "hello"), resident.RunHooks{BeforeSend: func(string, string) error { return sentinel }})
		if !errors.Is(err, sentinel) {
			t.Fatal(err)
		}
		restarted, err := New(cfg, dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer restarted.Close()
		if len(restarted.DirectSessions()) != 0 {
			t.Fatal("orphan session persisted")
		}
	})
	t.Run("停止は別の会話に影響しない", func(t *testing.T) {
		p, _, _ := testProvider(t)
		ordinary := startTest(t, p, "hold")
		ctx, cancel := context.WithCancelCause(context.Background())
		defer cancel(nil)
		sentinel := errors.New("task stopped")
		_, err := p.ResidentRunner().Run(ctx, residentRequestForTest(t, "hold"), resident.RunHooks{Sent: func() { cancel(sentinel) }})
		if !errors.Is(err, sentinel) {
			t.Fatal(err)
		}
		waitInfo(t, p, ordinary, func(i providers.DirectInfo) bool { return i.Alive && i.Running })
	})
	t.Run("待機はタスクの期限で終了する", func(t *testing.T) {
		p, _, _ := testProvider(t)
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		_, err := p.ResidentRunner().Run(ctx, residentRequestForTest(t, "hold"), resident.RunHooks{})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	})
}

func TestDSHモデル接続先を選択して保存し再開時も維持する(t *testing.T) {
	old, cfg, dir := testProvider(t)
	old.Close()
	cfg.Models = []ModelRoute{{ID: "openai-api", Name: "OpenAI API", Provider: "openai", Model: "gpt-5.4", Efforts: []string{"low", "high"}}}
	cfg.Model = "openai-api"
	p, err := New(cfg, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	catalog, _ := p.Models(context.Background())
	if catalog.Models[0].ID != "openai-api" || len(catalog.Models[0].Efforts) != 2 {
		t.Fatalf("catalog: %+v", catalog)
	}
	id := startTest(t, p, "")
	check := func() {
		b, err := os.ReadFile(filepath.Join(p.cfg.Home, "initialize.json"))
		if err != nil {
			t.Fatal(err)
		}
		var init map[string]any
		json.Unmarshal(b, &init)
		if init["provider"] != "openai" || init["model"] != "gpt-5.4" {
			t.Fatalf("route: %s", b)
		}
	}
	check()
	p.Close()
	cfg.Models[0].Provider = "other-route"
	cfg.Models[0].Model = "other-model"
	p, err = New(cfg, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err = p.DirectStart(context.Background(), providers.DirectStart{Resume: id, Model: cfg.Model}); err != nil {
		t.Fatal(err)
	}
	check()
	if err = p.DirectSetModel(context.Background(), id, "unknown-route"); err == nil {
		t.Fatal("unconfigured route accepted")
	}
}
