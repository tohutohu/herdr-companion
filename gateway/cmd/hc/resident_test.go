package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// residentGateway fakes the resident endpoints and records requests.
type residentGateway struct {
	mu      sync.Mutex
	tasks   []map[string]any
	reqs    []postedRequest
	methods []string
	missing bool // an old Gateway without resident tasks
}

func (g *residentGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var body map[string]any
	if b, _ := io.ReadAll(r.Body); len(b) > 0 {
		json.Unmarshal(b, &body)
	}
	g.reqs = append(g.reqs, postedRequest{r.URL.Path, body})
	g.methods = append(g.methods, r.Method)
	if g.missing {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/v1/resident/tasks" && r.Method == http.MethodGet:
		json.NewEncoder(w).Encode(map[string]any{"tasks": g.tasks})
	case r.URL.Path == "/v1/resident/tasks" && r.Method == http.MethodPost:
		t := map[string]any{"id": "task_abc12345", "name": body["name"], "schedule": body["schedule"], "version": 1, "state": "idle"}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(t)
	case strings.HasSuffix(r.URL.Path, "/runs") && r.Method == http.MethodPost:
		json.NewEncoder(w).Encode(map[string]any{"run": map[string]any{"id": "run_r1", "status": "queued"}})
	default:
		json.NewEncoder(w).Encode(map[string]any{"id": "task_abc12345", "name": "x", "version": 4, "state": "idle"})
	}
}

func newResidentApp(t *testing.T, g *residentGateway) (*app, *bytes.Buffer) {
	t.Helper()
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	out := &bytes.Buffer{}
	return &app{out: out, errOut: out, in: strings.NewReader(""),
		client: newClient(connection{baseURL: srv.URL, token: "secret"}),
		state:  &stateStore{dir: t.TempDir()}, selfID: "claude:self"}, out
}

func TestResidentCommands(t *testing.T) {
	t.Run("createはフラグから予定と権限と信頼確認を組み立てrequestIdを付ける", func(t *testing.T) {
		g := &residentGateway{}
		a, _ := newResidentApp(t, g)
		dir := t.TempDir()
		instr := filepath.Join(dir, "instr.md")
		os.WriteFile(instr, []byte("毎朝CIを確認して\n変化があれば知らせて"), 0o644)
		out := a.mustRun(t, "resident", "create", "--name", "CI監視", "--provider", "claude", "--cwd", dir,
			"--instruction-file", instr, "--weekly", "mon,thu@07:00", "--tz", "Europe/London",
			"--allow", "Bash(gh run list:*)", "--network", "--trust", "--model", "haiku")
		if !strings.Contains(out, "created abc12345") {
			t.Errorf("out = %s", out)
		}
		body := g.reqs[0].Body
		sched := body["schedule"].(map[string]any)
		perms := body["permissions"].(map[string]any)
		if sched["kind"] != "weekly" || sched["time"] != "07:00" || sched["timezone"] != "Europe/London" ||
			perms["network"] != true || perms["allowedTools"].([]any)[0] != "Bash(gh run list:*)" || body["trust"] != true ||
			!strings.Contains(body["instruction"].(string), "変化があれば") || body["requestId"] == "" || body["model"] != "haiku" {
			t.Errorf("body = %v", body)
		}
	})
	t.Run("予定のフラグを二つ指定すると使い方の誤りで終了コード2", func(t *testing.T) {
		a, out := newResidentApp(t, &residentGateway{})
		if code := a.run([]string{"resident", "create", "--name", "x", "--daily", "07:00", "--cron", "0 7 * * *"}); code != 2 {
			t.Errorf("exit %d: %s", code, out)
		}
	})
	t.Run("lsは判断待ちのタスクに印を付ける", func(t *testing.T) {
		g := &residentGateway{tasks: []map[string]any{
			{"id": "task_aaaa1111", "name": "CI", "state": "waiting_user", "provider": "codex", "unread": 2},
			{"id": "task_bbbb2222", "name": "メール", "state": "idle", "provider": "claude"},
		}}
		a, _ := newResidentApp(t, g)
		out := a.mustRun(t, "resident", "ls")
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if !strings.HasPrefix(lines[0], "! aaaa1111 waiting_user") || !strings.Contains(lines[0], "(2 unread)") || !strings.HasPrefix(lines[1], "  bbbb2222") {
			t.Errorf("out =\n%s", out)
		}
		js := a.mustRun(t, "resident", "ls", "--json")
		var parsed []map[string]any
		if json.Unmarshal([]byte(js), &parsed) != nil || len(parsed) != 2 {
			t.Errorf("json = %s", js)
		}
	})
	t.Run("updateは現在の版を送り名前でもタスクを指定できる", func(t *testing.T) {
		g := &residentGateway{tasks: []map[string]any{{"id": "task_aaaa1111", "name": "CI", "state": "idle", "version": 7}}}
		a, _ := newResidentApp(t, g)
		a.mustRun(t, "resident", "update", "CI", "--daily", "08:30")
		last := g.reqs[len(g.reqs)-1]
		if g.methods[len(g.methods)-1] != http.MethodPatch || last.Path != "/v1/resident/tasks/task_aaaa1111" || last.Body["expectedVersion"] != float64(7) {
			t.Errorf("request = %s %+v", g.methods[len(g.methods)-1], last)
		}
	})
	t.Run("runは手動実行を送り再試行用のrequestIdを付ける", func(t *testing.T) {
		g := &residentGateway{tasks: []map[string]any{{"id": "task_aaaa1111", "name": "CI", "state": "idle"}}}
		a, _ := newResidentApp(t, g)
		out := a.mustRun(t, "resident", "run", "aaaa", "--request-id", "fixed-request-1")
		last := g.reqs[len(g.reqs)-1]
		if last.Path != "/v1/resident/tasks/task_aaaa1111/runs" || last.Body["requestId"] != "fixed-request-1" || !strings.Contains(out, "queued run r1") {
			t.Errorf("out = %s, request = %+v", out, last)
		}
	})
	t.Run("常駐に対応しないGatewayでは更新を促して終了コード1", func(t *testing.T) {
		a, out := newResidentApp(t, &residentGateway{missing: true})
		if code := a.run([]string{"resident", "ls"}); code != 1 || !strings.Contains(out.String(), "update the Gateway") {
			t.Errorf("exit %d: %s", code, out)
		}
	})
	t.Run("deleteは確認フラグなしでは実行しない", func(t *testing.T) {
		g := &residentGateway{tasks: []map[string]any{{"id": "task_aaaa1111", "name": "CI"}}}
		a, _ := newResidentApp(t, g)
		if code := a.run([]string{"resident", "delete", "CI"}); code != 2 {
			t.Errorf("exit %d", code)
		}
		if len(g.reqs) != 0 {
			t.Error("a request was sent")
		}
	})
}
