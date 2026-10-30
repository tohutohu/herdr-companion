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
	for _, name := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\ncase \"$1\" in\n--version) echo test-1;;\nupdate|upgrade) echo updated;;\n*) exit 1;;\nesac\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := &Server{Config: store, AgentUpdates: agentupdate.New(ctx, filepath.Join(dir, "codex"))}
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
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "test-1") || !strings.Contains(string(body), `"provider":"claude"`) {
		t.Fatal(resp.StatusCode, string(body))
	}
	resp, body = do(t, ts, token, "POST", "/v1/agents/codex/update", nil, "")
	if resp.StatusCode != http.StatusAccepted || !strings.Contains(string(body), "running") {
		t.Fatal(resp.StatusCode, string(body))
	}
	for _, gone := range []string{"opencode", "devin"} {
		if resp, _ := do(t, ts, token, "POST", "/v1/agents/"+gone+"/update", nil, ""); resp.StatusCode != http.StatusBadRequest {
			t.Fatal(gone, resp.StatusCode)
		}
	}
}
