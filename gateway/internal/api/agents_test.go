package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/agentupdate"
	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
)

func Testエージェント更新は認証必須で固定のプロバイダーだけ受け付ける(t *testing.T) {
	store, err := config.Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{"claude", "codex", "opencode"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\ncase \"$1\" in\n--version) echo test-1;;\nupdate|upgrade) echo updated;;\n*) exit 1;;\nesac\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := &Server{Config: store, AgentUpdates: agentupdate.New(ctx, filepath.Join(dir, "codex"), filepath.Join(dir, "opencode"))}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	token := store.Get().AuthToken
	resp, _ := do(t, ts, "", "POST", "/v1/agents/codex/update", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatal(resp.StatusCode)
	}
	resp, _ = do(t, ts, token, "POST", "/v1/agents/unknown/update", nil, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatal(resp.StatusCode)
	}
	resp, body := do(t, ts, token, "GET", "/v1/agents", nil, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "test-1") || !strings.Contains(string(body), `"provider":"opencode"`) {
		t.Fatal(resp.StatusCode, string(body))
	}
	resp, body = do(t, ts, token, "POST", "/v1/agents/codex/update", nil, "")
	if resp.StatusCode != http.StatusAccepted || !strings.Contains(string(body), "running") {
		t.Fatal(resp.StatusCode, string(body))
	}
	resp, body = do(t, ts, token, "POST", "/v1/agents/opencode/update", nil, "")
	if resp.StatusCode != http.StatusAccepted || !strings.Contains(string(body), `"provider":"opencode"`) || !strings.Contains(string(body), "running") {
		t.Fatal(resp.StatusCode, string(body))
	}

}

func Test起動ペイン端末は認証と起動済みチェックとキー制限を使う(t *testing.T) {
	ts, _, _, token := newTestServer(t)
	resp, _ := do(t, ts, "", "GET", "/v1/launches/w2:p1/terminal", nil, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatal(resp.StatusCode)
	}
	resp, _ = do(t, ts, token, "GET", "/v1/launches/w1:p1/terminal", nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatal(resp.StatusCode)
	}
	resp, body := do(t, ts, token, "POST", "/v1/sessions/fake:old/resume", []byte(`{"trust":true}`), "application/json")
	if resp.StatusCode != http.StatusCreated {
		t.Fatal(resp.StatusCode, string(body))
	}
	resp, body = do(t, ts, token, "GET", "/v1/launches/w2:p1/terminal", nil, "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "$ hello") {
		t.Fatal(resp.StatusCode, string(body))
	}
	resp, _ = do(t, ts, token, "POST", "/v1/launches/w2:p1/terminal", []byte(`{"keys":["enter"]}`), "application/json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatal(resp.StatusCode)
	}
	resp, _ = do(t, ts, token, "POST", "/v1/launches/w2:p1/terminal", []byte(`{"keys":["ctrl+z"]}`), "application/json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatal(resp.StatusCode)
	}
}
