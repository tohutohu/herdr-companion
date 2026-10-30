package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/resident"
)

func residentRequest(prompt string, perms resident.Permissions) resident.RunRequest {
	return resident.RunRequest{RunID: "run_1", Cwd: "/work", Model: "sonnet", Effort: "high", Prompt: prompt,
		Schema: resident.ResultSchema, Permissions: perms}
}

func hooks(sent *atomic.Bool) resident.RunHooks {
	return resident.RunHooks{
		BeforeSend: func(string, string) error { return nil },
		Sent:       func() { sent.Store(true) },
	}
}

func Test常駐実行はtranscriptを残し構造化出力と権限プロファイルとモデルを渡す(t *testing.T) {
	p, log := newSidecarProvider(t)
	var notified atomic.Int32
	p.SetDirectListener(func(string) { notified.Add(1) })
	r := p.ResidentRunner()
	t.Cleanup(r.Close)
	var sent atomic.Bool
	out, err := r.Run(context.Background(), residentRequest("check", resident.Permissions{Profile: resident.ProfileReadOnly, AllowedTools: []string{"Bash(gh pr view:*)"}}), hooks(&sent))
	if err != nil {
		t.Fatal(err)
	}
	if !sent.Load() || string(out.Structured) == "" || out.Model != "claude-sonnet-5-5" || out.Usage.CostUSD != 0.02 || out.Usage.CachedInputTokens != 50 {
		t.Errorf("out = %+v", out)
	}
	starts := sidecarRequests(t, log, "start")
	if len(starts) != 1 {
		t.Fatalf("starts = %v", starts)
	}
	s := starts[0]
	if s["resident"] != true || s["persist"] != true || s["model"] != "sonnet" || s["effort"] != "high" || s["permissionMode"] != "default" {
		t.Errorf("start = %v", s)
	}
	of, _ := s["outputFormat"].(map[string]any)
	if of["type"] != "json_schema" || of["schema"] == nil {
		t.Errorf("outputFormat = %v", of)
	}
	allowed := toStrings(s["allowedTools"])
	disallowed := toStrings(s["disallowedTools"])
	if !slices.Contains(allowed, "Bash(gh pr view:*)") || !slices.Contains(disallowed, "Edit") || !slices.Contains(allowed, "WebFetch") {
		t.Errorf("allowed = %v, disallowed = %v", allowed, disallowed)
	}
	env, _ := s["env"].(map[string]any)
	if env["HC_RESIDENT_RUN"] != "run_1" || env["HC_SESSION_ID"] != nil {
		t.Errorf("env = %v", env)
	}
	if len(sidecarRequests(t, log, "stop")) != 1 {
		t.Error("the run's Claude Code process was not ended")
	}
	if n := len(p.DirectSessions()); n != 0 || notified.Load() != 0 {
		t.Errorf("the run reached the session runtime: %d sessions, %d notifications", n, notified.Load())
	}
}

func Test書き込みプロファイルはacceptEditsでWeb系ツールは常に許す(t *testing.T) {
	mode, allowed, disallowed := residentPermissions(resident.Permissions{Profile: resident.ProfileWorkspaceWrite})
	if mode != "acceptEdits" || !slices.Contains(allowed, "WebFetch") || slices.Contains(disallowed, "Edit") || slices.Contains(disallowed, "WebFetch") {
		t.Errorf("mode=%s allowed=%v disallowed=%v", mode, allowed, disallowed)
	}
	mode, _, _ = residentPermissions(resident.Permissions{Profile: resident.ProfileReadOnly})
	if mode == "bypassPermissions" || mode == "dontAsk" || mode == "auto" {
		t.Errorf("mode = %s", mode)
	}
}

func Test編集可のタスクはモデルが対応していればautoモードで実行する(t *testing.T) {
	p, log := newSidecarProvider(t)
	r := p.ResidentRunner()
	var sent atomic.Bool
	// sonnet supports auto mode in the fake sidecar's model list.
	if _, err := r.Run(context.Background(), residentRequest("check", resident.Permissions{Profile: resident.ProfileWorkspaceWrite}), hooks(&sent)); err != nil {
		t.Fatal(err)
	}
	modes := sidecarRequests(t, log, "setMode")
	if len(modes) != 1 || modes[0]["mode"] != "auto" {
		t.Errorf("setMode = %v", modes)
	}
	req := residentRequest("check", resident.Permissions{Profile: resident.ProfileWorkspaceWrite})
	req.Model = "claude-haiku-4-5-20251001"
	if _, err := r.Run(context.Background(), req, hooks(&sent)); err != nil {
		t.Fatal(err)
	}
	if n := len(sidecarRequests(t, log, "setMode")); n != 1 {
		t.Errorf("a model without auto mode was switched to it (%d setMode calls)", n)
	}
}

func Test読み取り専用のタスクはautoモードにしない(t *testing.T) {
	p, log := newSidecarProvider(t)
	r := p.ResidentRunner()
	var sent atomic.Bool
	if _, err := r.Run(context.Background(), residentRequest("check", resident.Permissions{Profile: resident.ProfileReadOnly}), hooks(&sent)); err != nil {
		t.Fatal(err)
	}
	if len(sidecarRequests(t, log, "setMode")) != 0 {
		t.Error("a read-only run was switched to auto mode")
	}
}

func Test許可外のツールの拒否を結果に含める(t *testing.T) {
	p, _ := newSidecarProvider(t)
	r := p.ResidentRunner()
	t.Cleanup(r.Close)
	var sent atomic.Bool
	out, err := r.Run(context.Background(), residentRequest("deny please", resident.Permissions{Profile: resident.ProfileReadOnly}), hooks(&sent))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.PermissionDenials) != 1 || out.PermissionDenials[0] != "Bash(gh run list)" {
		t.Errorf("denials = %v", out.PermissionDenials)
	}
}

func Test未ログインのClaudeでは常駐実行を送信せずに拒否する(t *testing.T) {
	p, log := newSidecarProvider(t)
	t.Setenv("HERDR_FAKE_SIDECAR_NOLOGIN", "1")
	r := p.ResidentRunner()
	t.Cleanup(r.Close)
	var sent atomic.Bool
	_, err := r.Run(context.Background(), residentRequest("check", resident.Permissions{Profile: resident.ProfileReadOnly}), resident.RunHooks{
		BeforeSend: func(string, string) error { t.Error("BeforeSend for a refused login"); return nil },
		Sent:       func() { sent.Store(true) },
	})
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("err = %v", err)
	}
	if len(sidecarRequests(t, log, "send")) != 0 {
		t.Error("the prompt was sent")
	}
}

func Test提供されないモデルは置き換えずに拒否する(t *testing.T) {
	p, log := newSidecarProvider(t)
	r := p.ResidentRunner()
	t.Cleanup(r.Close)
	req := residentRequest("check", resident.Permissions{Profile: resident.ProfileReadOnly})
	req.Model = "opus"
	var sent atomic.Bool
	if _, err := r.Run(context.Background(), req, hooks(&sent)); err == nil || !strings.Contains(err.Error(), "not offered") {
		t.Errorf("err = %v", err)
	}
	if len(sidecarRequests(t, log, "send")) != 0 {
		t.Error("the prompt was sent")
	}
}

func Test常駐実行の停止は当該実行だけを終え通常セッションを止めない(t *testing.T) {
	p, log := newSidecarProvider(t)
	sid, err := p.DirectStart(context.Background(), providers.DirectStart{Cwd: "/work"})
	if err != nil {
		t.Fatal(err)
	}
	r := p.ResidentRunner()
	t.Cleanup(r.Close)
	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan error, 1)
	var sent atomic.Bool
	go func() {
		_, err := r.Run(ctx, residentRequest("hang", resident.Permissions{Profile: resident.ProfileReadOnly}), hooks(&sent))
		done <- err
	}()
	eventually(t, "the run to be sent", sent.Load)
	stopCause := errors.New("stopped by the user")
	cancel(stopCause)
	select {
	case err := <-done:
		if !errors.Is(err, stopCause) {
			t.Errorf("err = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the run did not stop")
	}
	for _, s := range sidecarRequests(t, log, "stop") {
		if s["sessionId"] == sid {
			t.Error("the normal session was stopped")
		}
	}
	if !info(p, sid).Alive {
		t.Error("the normal session is no longer alive")
	}
	if err := p.Send(context.Background(), sid, &providers.Live{State: providers.StateIdle}, model.Input{Text: "hello"}); err != nil {
		t.Errorf("the session cannot take a message: %v", err)
	}
}

func toStrings(v any) []string {
	var out []string
	list, _ := v.([]any)
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func Test常駐実行のtranscriptに題名を付けると一覧の題名になり削除するとサブエージェントごと消える(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "projects", "-work")
	id := "0b6c7a43-1f5e-4a3c-9d0e-2b8f4c1a7e55"
	if err := os.MkdirAll(filepath.Join(project, id, "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(project, id+".jsonl")
	line := `{"type":"user","sessionId":"` + id + `","cwd":"/work","timestamp":"2026-10-30T07:00:00Z","message":{"role":"user","content":"check"}}` + "\n"
	if err := os.WriteFile(transcript, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	p := New(dir, deadletter.Nop{})
	r := p.ResidentRunner()
	if err := r.Rename(context.Background(), id, "CI監視（常駐 10/30 07:00）"); err != nil {
		t.Fatal(err)
	}
	sum, err := p.Summary(context.Background(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Title != "CI監視（常駐 10/30 07:00）" {
		t.Errorf("title = %q", sum.Title)
	}
	if err := r.Discard(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(transcript); !os.IsNotExist(err) {
		t.Errorf("transcript remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, id)); !os.IsNotExist(err) {
		t.Errorf("subagent directory remains: %v", err)
	}
	if err := r.Discard(context.Background(), id); !errors.Is(err, providers.ErrNotFound) {
		t.Errorf("second discard = %v", err)
	}
}
