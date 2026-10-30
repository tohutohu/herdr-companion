package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/resident"
)

type apiRunner struct{ calls int }

func (r *apiRunner) Run(ctx context.Context, req resident.RunRequest, hooks resident.RunHooks) (*resident.RunOutput, error) {
	r.calls++
	if err := hooks.BeforeSend("native-"+req.RunID, "m"); err != nil {
		return nil, err
	}
	hooks.Sent()
	b, _ := json.Marshal(map[string]any{"schemaVersion": 1, "outcome": "changed", "summary": "CIが復旧しました", "report": "詳細",
		"notification": map[string]any{"requested": true, "key": "ci:1"}, "statePatch": []any{}, "memorySummary": nil,
		"nextWakeAt": nil, "question": nil, "artifacts": []any{}})
	return &resident.RunOutput{NativeID: "native-" + req.RunID, Structured: b}, nil
}

type apiEnv struct{ root string }

func (e apiEnv) ResolveDir(p string) (string, error) {
	if !strings.HasPrefix(p, e.root) {
		return "", os.ErrPermission
	}
	return p, nil
}
func (e apiEnv) FolderTrusted(string, string) bool { return false }
func (e apiEnv) Catalog(context.Context, string) (providers.ModelCatalog, error) {
	return providers.ModelCatalog{}, nil
}

func withResident(t *testing.T, runner *apiRunner) (func(*Server), **resident.Service, string) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	var svc *resident.Service
	return func(s *Server) {
		st, err := resident.Open(filepath.Join(t.TempDir(), "resident.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		svc = resident.New(st, apiEnv{root}, map[string]resident.Runner{"claude": runner, "codex": runner}, resident.Config{}, nil)
		t.Cleanup(func() { svc.Shutdown(time.Second) })
		s.Resident = svc
	}, &svc, root
}

func taskBody(cwd string, extra map[string]any) []byte {
	m := map[string]any{"name": "CI監視", "instruction": "CIを確認", "provider": "claude", "cwd": cwd,
		"schedule": map[string]any{"kind": "daily", "time": "07:00"}, "trust": true}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return b
}

func Test機能一覧で常駐APIの対応を判別できる(t *testing.T) {
	ts, _, _, tok := newTestServer(t)
	_, body := do(t, ts, tok, "GET", "/v1/capabilities", nil, "")
	if strings.Contains(string(body), "resident") {
		t.Errorf("a gateway without resident tasks advertised them: %s", body)
	}
	if resp, _ := do(t, ts, tok, "GET", "/v1/resident/tasks", nil, ""); resp.StatusCode != 404 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	opt, _, _ := withResident(t, &apiRunner{})
	ts2, _, _, tok2 := newTestServer(t, opt)
	_, body = do(t, ts2, tok2, "GET", "/v1/capabilities", nil, "")
	var caps struct {
		Resident struct {
			Version int `json:"version"`
		} `json:"resident"`
	}
	json.Unmarshal(body, &caps)
	if caps.Resident.Version != ResidentAPIVersion {
		t.Errorf("capabilities = %s", body)
	}
}

func Test常駐APIは認証が必要(t *testing.T) {
	opt, _, _ := withResident(t, &apiRunner{})
	ts, _, _, _ := newTestServer(t, opt)
	for _, path := range []string{"/v1/resident/tasks", "/v1/capabilities"} {
		if resp, _ := do(t, ts, "wrong", "GET", path, nil, ""); resp.StatusCode != 401 {
			t.Errorf("%s: status %d", path, resp.StatusCode)
		}
	}
}

func Test常駐タスクを作成し実行して履歴と会話と通知を確認できる(t *testing.T) {
	runner := &apiRunner{}
	opt, svc, root := withResident(t, runner)
	ts, _, _, tok := newTestServer(t, opt)
	resp, body := do(t, ts, tok, "POST", "/v1/resident/tasks", taskBody(root, map[string]any{"requestId": "create-0001"}), "application/json")
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	var task resident.Task
	json.Unmarshal(body, &task)
	if task.ID == "" || task.NextRunAt == nil || task.Schedule.Timezone != "Asia/Tokyo" {
		t.Fatalf("task = %s", body)
	}
	// The same create request again returns the same task.
	_, body2 := do(t, ts, tok, "POST", "/v1/resident/tasks", taskBody(root, map[string]any{"requestId": "create-0001"}), "application/json")
	var again resident.Task
	json.Unmarshal(body2, &again)
	if again.ID != task.ID {
		t.Errorf("a retried create made task %s", again.ID)
	}
	resp, body = do(t, ts, tok, "POST", "/v1/resident/tasks/"+task.ID+"/runs", []byte(`{"requestId":"run-00000001"}`), "application/json")
	if resp.StatusCode != 202 {
		t.Fatalf("run now: %d %s", resp.StatusCode, body)
	}
	do(t, ts, tok, "POST", "/v1/resident/tasks/"+task.ID+"/runs", []byte(`{"requestId":"run-00000001"}`), "application/json")
	(*svc).Tick(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for len((*svc).ActiveRuns()) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	_, body = do(t, ts, tok, "GET", "/v1/resident/tasks/"+task.ID+"/runs", nil, "")
	var runs struct{ Runs []resident.Run }
	json.Unmarshal(body, &runs)
	if len(runs.Runs) != 1 || runs.Runs[0].Status != resident.RunCompleted || runner.calls != 1 {
		t.Fatalf("runs = %s (calls %d)", body, runner.calls)
	}
	_, body = do(t, ts, tok, "GET", "/v1/resident/tasks/"+task.ID+"/messages", nil, "")
	if !strings.Contains(string(body), "詳細") {
		t.Errorf("messages = %s", body)
	}
	_, body = do(t, ts, tok, "GET", "/v1/resident/tasks/"+task.ID, nil, "")
	var detail struct {
		Task     resident.Task
		Upcoming []time.Time
		Memory   resident.Memory
	}
	json.Unmarshal(body, &detail)
	if len(detail.Upcoming) != 3 || detail.Task.LatestReport == nil || detail.Task.Unread != 1 {
		t.Errorf("detail = %s", body)
	}
	do(t, ts, tok, "POST", "/v1/resident/tasks/"+task.ID+"/read", []byte(`{}`), "application/json")
	_, body = do(t, ts, tok, "GET", "/v1/resident/tasks/"+task.ID+"/notifications", nil, "")
	if !strings.Contains(string(body), `"kind":"change"`) {
		t.Errorf("notifications = %s", body)
	}
	_, body = do(t, ts, tok, "GET", "/v1/resident/runs/"+runs.Runs[0].ID, nil, "")
	if !strings.Contains(string(body), `"attempts"`) {
		t.Errorf("run = %s", body)
	}
}

func Test常駐APIは不正な設定を理由付きで拒否する(t *testing.T) {
	opt, _, root := withResident(t, &apiRunner{})
	ts, _, _, tok := newTestServer(t, opt)
	cases := []struct {
		name   string
		body   []byte
		status int
		code   string
		want   string
	}{
		{"無効なcron", taskBody(root, map[string]any{"schedule": map[string]any{"kind": "cron", "cron": "0 7 * *"}}), 400, "invalid", "5 fields"},
		{"無効なtimezone", taskBody(root, map[string]any{"schedule": map[string]any{"kind": "daily", "time": "07:00", "timezone": "Mars/Base"}}), 400, "invalid", "unknown timezone"},
		{"範囲外のcwd", taskBody("/etc", nil), 400, "invalid", "workspace roots"},
		{"未信頼フォルダ", taskBody(root, map[string]any{"trust": false}), 409, "trust_required", "never trusted"},
		{"未知の項目", taskBody(root, map[string]any{"bogus": 1}), 400, "invalid", "unknown field"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, body := do(t, ts, tok, "POST", "/v1/resident/tasks", c.body, "application/json")
			var e struct{ Error, Code string }
			json.Unmarshal(body, &e)
			if resp.StatusCode != c.status || e.Code != c.code || !strings.Contains(e.Error, c.want) {
				t.Errorf("%d %s", resp.StatusCode, body)
			}
		})
	}
	resp, body := do(t, ts, tok, "POST", "/v1/resident/schedule/preview", []byte(`{"schedule":{"kind":"weekly","time":"07:00","days":["mon"]}}`), "application/json")
	var prev struct{ Next []time.Time }
	json.Unmarshal(body, &prev)
	if resp.StatusCode != 200 || len(prev.Next) != 3 || prev.Next[0].Weekday() != time.Sunday { // 07:00 JST Monday is Sunday 22:00 UTC
		t.Errorf("preview = %d %s", resp.StatusCode, body)
	}
}

func Test常駐の実行は通常のセッション一覧に混ざらない(t *testing.T) {
	opt, _, _ := withResident(t, &apiRunner{})
	ts, _, _, tok := newTestServer(t, opt, func(s *Server) {
		s.Sessions.Hidden = func(context.Context) map[string]bool { return map[string]bool{"fake:old": true} }
	})
	_, body := do(t, ts, tok, "GET", "/v1/sessions", nil, "")
	var got struct{ Sessions []model.Session }
	json.Unmarshal(body, &got)
	for _, s := range got.Sessions {
		if s.ID == "fake:old" {
			t.Error("a hidden resident run is listed as a session")
		}
	}
	if len(got.Sessions) != 1 {
		t.Errorf("sessions = %+v", got.Sessions)
	}
}

func Test常駐の実行をセッションに昇格するAPI(t *testing.T) {
	runner := &apiRunner{}
	opt, svc, root := withResident(t, runner)
	ts, _, _, tok := newTestServer(t, opt)
	_, body := do(t, ts, tok, "POST", "/v1/resident/tasks", taskBody(root, nil), "application/json")
	var task resident.Task
	json.Unmarshal(body, &task)
	_, body = do(t, ts, tok, "POST", "/v1/resident/tasks/"+task.ID+"/runs", []byte(`{}`), "application/json")
	var posted resident.PostResult
	json.Unmarshal(body, &posted)

	// 終わっていない実行は昇格できない
	resp, body := do(t, ts, tok, "POST", "/v1/resident/runs/"+posted.Run.ID+"/promote", nil, "")
	if resp.StatusCode != 409 || !strings.Contains(string(body), "conflict") {
		t.Errorf("queued: %d %s", resp.StatusCode, body)
	}
	(*svc).Tick(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for len((*svc).ActiveRuns()) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	_, body = do(t, ts, tok, "GET", "/v1/resident/tasks/"+task.ID+"/messages", nil, "")
	if !strings.Contains(string(body), `"conversation":"kept"`) {
		t.Errorf("messages = %s", body)
	}
	// このテストの Gateway には claude が無いので再開は 404 になるが、昇格は記録される
	resp, body = do(t, ts, tok, "POST", "/v1/resident/runs/"+posted.Run.ID+"/promote", nil, "")
	if resp.StatusCode != 404 {
		t.Errorf("promote: %d %s", resp.StatusCode, body)
	}
	_, body = do(t, ts, tok, "GET", "/v1/resident/runs/"+posted.Run.ID, nil, "")
	if !strings.Contains(string(body), `"conversation":"promoted"`) || !strings.Contains(string(body), `"sessionId":"claude:native-`+posted.Run.ID+`"`) {
		t.Errorf("run = %s", body)
	}
	resp, body = do(t, ts, tok, "POST", "/v1/resident/runs/run_unknown/promote", nil, "")
	if resp.StatusCode != 404 || !strings.Contains(string(body), "not_found") {
		t.Errorf("unknown: %d %s", resp.StatusCode, body)
	}
}
