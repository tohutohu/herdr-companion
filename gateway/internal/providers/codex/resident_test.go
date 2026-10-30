package codex

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/resident"
)

// residentServer is a fake app-server for resident runs.
type residentServer struct {
	pt    *pipeTransport
	model string
	mode  string // "answer", "hang", "approval", "review"
	mu    sync.Mutex
	calls []directCall
	resps []map[string]any
}

const residentThread = "019a0000-0000-7000-8000-00000000r001"

func (s *residentServer) send(v any) {
	b, _ := json.Marshal(v)
	s.pt.in <- b
}

func (s *residentServer) serve() {
	for {
		var b []byte
		select {
		case b = <-s.pt.out:
		case <-s.pt.closed:
			return
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
			Result map[string]any  `json:"result"`
		}
		json.Unmarshal(b, &m)
		if m.Method == "" {
			s.mu.Lock()
			s.resps = append(s.resps, m.Result)
			s.mu.Unlock()
			continue
		}
		s.mu.Lock()
		s.calls = append(s.calls, directCall{m.Method, m.Params})
		s.mu.Unlock()
		var result any = map[string]any{}
		switch m.Method {
		case "thread/start":
			result = map[string]any{"thread": map[string]any{"id": residentThread, "cwd": "/work"}, "model": s.model, "cwd": "/work"}
		case "turn/start":
			result = map[string]any{"turn": map[string]any{"id": "turn-r1", "status": "inProgress"}}
		}
		if len(m.ID) > 0 {
			s.send(map[string]any{"id": json.RawMessage(m.ID), "result": result})
		}
		if m.Method == "turn/start" {
			go s.play()
		}
		if m.Method == "turn/interrupt" {
			s.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": residentThread, "turn": map[string]any{"id": "turn-r1", "status": "interrupted"}}})
		}
	}
}

func (s *residentServer) play() {
	n := func(method string, params map[string]any) {
		params["threadId"] = residentThread
		s.send(map[string]any{"method": method, "params": params})
	}
	switch s.mode {
	case "hang":
		return
	case "approval":
		s.send(map[string]any{"id": 77, "method": "item/commandExecution/requestApproval", "params": map[string]any{"threadId": residentThread, "command": "git push"}})
	case "review":
		n("item/autoApprovalReview/completed", map[string]any{"reviewId": "rv1", "action": map[string]any{"type": "command", "command": "touch /etc/x", "cwd": "/work", "source": "shell"},
			"review": map[string]any{"status": "approved", "riskLevel": "low"}})
		n("item/autoApprovalReview/completed", map[string]any{"reviewId": "rv2", "action": map[string]any{"type": "command", "command": "git push --force", "cwd": "/work", "source": "shell"},
			"review": map[string]any{"status": "denied", "riskLevel": "high", "rationale": "履歴を書き換える"}})
	}
	n("item/completed", map[string]any{"item": map[string]any{"type": "agentMessage", "text": `{"schemaVersion":1,"outcome":"unchanged","summary":"変化なし"}`}})
	n("thread/tokenUsage/updated", map[string]any{"turnId": "turn-r1", "tokenUsage": map[string]any{"total": map[string]any{"inputTokens": 120, "cachedInputTokens": 40, "outputTokens": 30}}})
	n("turn/completed", map[string]any{"turn": map[string]any{"id": "turn-r1", "status": "completed"}})
}

func (s *residentServer) last(method string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.calls) - 1; i >= 0; i-- {
		if s.calls[i].Method == method {
			return s.calls[i].Params
		}
	}
	return nil
}

func newResidentRunner(t *testing.T, mode, model string) (*ResidentRunner, *residentServer) {
	t.Helper()
	pt := &pipeTransport{in: make(chan []byte, 64), out: make(chan []byte, 64), closed: make(chan struct{})}
	srv := &residentServer{pt: pt, mode: mode, model: model}
	go srv.serve()
	r := (&Provider{bin: "codex-not-used"}).ResidentRunner()
	r.dial = func() (transport, int, error) { return pt, 0, nil }
	return r, srv
}

func req(model string, perms resident.Permissions) resident.RunRequest {
	return resident.RunRequest{RunID: "run_c", Cwd: "/work", Model: model, Effort: "low", Prompt: "check", Schema: resident.ResultSchema, Permissions: perms}
}

func noHooks(sent *atomic.Bool) resident.RunHooks {
	return resident.RunHooks{BeforeSend: func(string, string) error { return nil }, Sent: func() { sent.Store(true) }}
}

func Test常駐実行は一時スレッドで承認なしのサンドボックスと出力スキーマを渡す(t *testing.T) {
	r, srv := newResidentRunner(t, "answer", "gpt-6-luna")
	var sent atomic.Bool
	out, err := r.Run(context.Background(), req("gpt-6-luna", resident.Permissions{Profile: resident.ProfileReadOnly}), noHooks(&sent))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text, `"outcome":"unchanged"`) || out.Usage.InputTokens != 120 || out.Usage.CachedInputTokens != 40 || out.Model != "gpt-6-luna" || out.NativeID != residentThread {
		t.Errorf("out = %+v", out)
	}
	ts := srv.last("thread/start")
	if ts["ephemeral"] != false || ts["approvalPolicy"] != "never" || ts["approvalsReviewer"] != nil || ts["sandbox"] != "read-only" || ts["model"] != "gpt-6-luna" {
		t.Errorf("thread/start = %v", ts)
	}
	tu := srv.last("turn/start")
	pol, _ := tu["sandboxPolicy"].(map[string]any)
	if tu["outputSchema"] == nil || tu["effort"] != "low" || tu["approvalPolicy"] != "never" || pol["type"] != "readOnly" || pol["networkAccess"] != true {
		t.Errorf("turn/start = %v", tu)
	}
	if srv.last("thread/unsubscribe") == nil {
		t.Error("the thread was not unloaded")
	}
}

func Test編集できるタスクは承認をauto_reviewに任せて却下を記録する(t *testing.T) {
	r, srv := newResidentRunner(t, "review", "gpt-6-luna")
	var sent atomic.Bool
	out, err := r.Run(context.Background(), req("gpt-6-luna", resident.Permissions{Profile: resident.ProfileWorkspaceWrite}), noHooks(&sent))
	if err != nil {
		t.Fatal(err)
	}
	ts := srv.last("thread/start")
	if ts["approvalPolicy"] != "on-request" || ts["approvalsReviewer"] != "auto_review" || ts["sandbox"] != "workspace-write" {
		t.Errorf("thread/start = %v", ts)
	}
	tu := srv.last("turn/start")
	pol, _ := tu["sandboxPolicy"].(map[string]any)
	if tu["approvalPolicy"] != "on-request" || tu["approvalsReviewer"] != "auto_review" || pol["type"] != "workspaceWrite" {
		t.Errorf("turn/start = %v", tu)
	}
	want := []string{"auto review denied git push --force: 履歴を書き換える"}
	if !slices.Equal(out.PermissionDenials, want) {
		t.Errorf("denials = %q, want %q", out.PermissionDenials, want)
	}
}

func Test承認要求は自動承認せず拒否して記録する(t *testing.T) {
	r, srv := newResidentRunner(t, "approval", "gpt-6-luna")
	var sent atomic.Bool
	out, err := r.Run(context.Background(), req("", resident.Permissions{Profile: resident.ProfileWorkspaceWrite}), noHooks(&sent))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.PermissionDenials) != 1 || !strings.Contains(out.PermissionDenials[0], "git push") {
		t.Errorf("denials = %v", out.PermissionDenials)
	}
	srv.mu.Lock()
	resps := append([]map[string]any(nil), srv.resps...)
	srv.mu.Unlock()
	if len(resps) != 1 || resps[0]["decision"] != "decline" {
		t.Errorf("responses = %v", resps)
	}
	if srv.last("thread/start")["sandbox"] != "workspace-write" {
		t.Error("the write profile did not choose workspace-write")
	}
}

func Test別のモデルで動く場合は置き換えずに失敗する(t *testing.T) {
	r, srv := newResidentRunner(t, "answer", "gpt-5")
	var sent atomic.Bool
	_, err := r.Run(context.Background(), req("gpt-6-luna", resident.Permissions{Profile: resident.ProfileReadOnly}), noHooks(&sent))
	if err == nil || !strings.Contains(err.Error(), "not replaced") || sent.Load() {
		t.Errorf("err = %v, sent = %v", err, sent.Load())
	}
	if srv.last("turn/start") != nil {
		t.Error("a turn was started")
	}
}

func Test停止するとターンを中断してスレッドを外す(t *testing.T) {
	r, srv := newResidentRunner(t, "hang", "gpt-6-luna")
	ctx, cancel := context.WithCancelCause(context.Background())
	var sent atomic.Bool
	done := make(chan error, 1)
	go func() {
		_, err := r.Run(ctx, req("", resident.Permissions{Profile: resident.ProfileReadOnly}), noHooks(&sent))
		done <- err
	}()
	waitFor(t, "the turn", sent.Load)
	cause := errors.New("stop")
	cancel(cause)
	select {
	case err := <-done:
		if !errors.Is(err, cause) {
			t.Errorf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not stop")
	}
	if srv.last("turn/interrupt") == nil || srv.last("thread/unsubscribe") == nil {
		t.Error("the turn was not interrupted and unloaded")
	}
}

func Test常駐用のapp_serverは通常のスレッド一覧の接続と別(t *testing.T) {
	p := New("codex-not-used", nil)
	r := p.ResidentRunner()
	if p.currentOwned() != nil {
		t.Fatal("unexpected connection")
	}
	pt := &pipeTransport{in: make(chan []byte, 64), out: make(chan []byte, 64), closed: make(chan struct{})}
	srv := &residentServer{pt: pt, mode: "answer", model: "m"}
	go srv.serve()
	r.dial = func() (transport, int, error) { return pt, 0, nil }
	var sent atomic.Bool
	if _, err := r.Run(context.Background(), req("", resident.Permissions{Profile: resident.ProfileReadOnly}), noHooks(&sent)); err != nil {
		t.Fatal(err)
	}
	if p.currentOwned() != nil || len(p.DirectSessions()) != 0 {
		t.Error("the resident run used or registered with the session app-server")
	}
}
