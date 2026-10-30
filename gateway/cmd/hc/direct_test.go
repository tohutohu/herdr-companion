package main

import (
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

func directGateway() *fakeGateway {
	g := sampleGateway()
	g.sessions = append(g.sessions, model.Session{
		ID: "claude:dddd4444-0000", Provider: "claude", Project: "app", Status: model.StatusIdle,
		UpdatedAt: t0, CanSend: true, Model: "claude-haiku-5-5", Effort: "low",
	})
	return g
}

func Test自分自身はエージェントが報告するセッションIDで判定する(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	if got := selfSession(get); got != "" {
		t.Fatalf("empty env: %q", got)
	}
	env["CODEX_THREAD_ID"] = "t1"
	if got := selfSession(get); got != "codex:t1" {
		t.Errorf("codex: %q", got)
	}
	env["CLAUDE_CODE_SESSION_ID"] = "c1"
	if got := selfSession(get); got != "claude:c1" {
		t.Errorf("claude: %q", got)
	}
	env["HC_SESSION_ID"] = "claude:gw"
	if got := selfSession(get); got != "claude:gw" {
		t.Errorf("gateway: %q", got)
	}
	if defaultCursor("claude:gw") != "session-claude:gw" || defaultCursor("") != "default" {
		t.Error("cursor")
	}
}

func Test自分自身には送信もアーカイブも中断もしない(t *testing.T) {
	g := directGateway()
	a, out := newTestApp(t, g)
	a.selfID = "claude:dddd4444-0000"
	for _, args := range [][]string{{"send", "dddd4444", "hi"}, {"archive", "dddd4444"}, {"interrupt", "dddd4444"}} {
		out.Reset()
		if code := a.run(args); code != 1 || !strings.Contains(out.String(), "yourself") {
			t.Fatalf("%v: code %d: %s", args, code, out.String())
		}
	}
	if len(g.posted) != 0 {
		t.Errorf("posted %v", g.posted)
	}
}

func Test起動はセッションIDをすぐに表示する(t *testing.T) {
	g := directGateway()
	g.replies = map[string]any{"/v1/sessions": map[string]any{"sessionId": "claude:eeee5555-0000"}}
	a, _ := newTestApp(t, g)
	got := a.mustRun(t, "start", "--cwd", "/tmp/x", "--model", "haiku", "hello")
	if !strings.Contains(got, "started claude:eeee5555-0000") || strings.Contains(got, "pane") {
		t.Errorf("output:\n%s", got)
	}
	if body := g.posted[0].Body; body["backend"] != nil || body["model"] != "haiku" || body["trust"] != true {
		t.Errorf("body %v", body)
	}
}

func Test信頼されていないフォルダの起動は理由を示す(t *testing.T) {
	g := directGateway()
	g.replies = map[string]any{"/v1/sessions": map[string]any{"launchId": "launch-claude-1", "trustRequired": true}}
	a, _ := newTestApp(t, g)
	got := a.mustRun(t, "start", "--cwd", "/tmp/x", "--no-trust", "hello")
	if !strings.Contains(got, "never trusted") || strings.Contains(got, "hc term") {
		t.Errorf("output:\n%s", got)
	}
}

func Testモデル変更は次のターンから適用されることを示す(t *testing.T) {
	g := directGateway()
	g.replies = map[string]any{"/v1/sessions/claude:dddd4444-0000/model": model.Session{
		ID: "claude:dddd4444-0000", Model: "claude-haiku-5-5", PendingModel: "sonnet", SettingsApply: "next_request",
	}}
	a, _ := newTestApp(t, g)
	got := a.mustRun(t, "model", "dddd4444", "sonnet")
	if !strings.Contains(got, "claude-haiku-5-5 → sonnet from the next model request") {
		t.Errorf("output:\n%s", got)
	}
	if p := g.posted[0]; p.Path != "/v1/sessions/claude:dddd4444-0000/model" || p.Body["model"] != "sonnet" {
		t.Errorf("posted %v", p)
	}
	g.replies["/v1/sessions/claude:dddd4444-0000/effort"] = model.Session{Effort: "low", PendingEffort: "high", SettingsApply: "next_turn"}
	got = a.mustRun(t, "effort", "dddd4444", "high")
	if !strings.Contains(got, "low → high from the next turn") {
		t.Errorf("output:\n%s", got)
	}
}

func Testターミナル操作と移行のコマンドはない(t *testing.T) {
	g := directGateway()
	a, out := newTestApp(t, g)
	for _, cmd := range []string{"term", "keys", "migrate"} {
		out.Reset()
		if code := a.run([]string{cmd, "dddd4444"}); code != 2 {
			t.Errorf("%s: code %d: %s", cmd, code, out.String())
		}
	}
	if len(g.posted) != 0 {
		t.Errorf("posted %v", g.posted)
	}
}

func Testモードはモード名を指定して設定できる(t *testing.T) {
	g := directGateway()
	a, _ := newTestApp(t, g)
	a.mustRun(t, "mode", "dddd4444", "plan")
	if p := g.posted[0]; p.Path != "/v1/sessions/claude:dddd4444-0000/mode" || p.Body["mode"] != "plan" {
		t.Errorf("posted %v", p)
	}
}
