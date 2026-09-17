package usage

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

const sample = `[
  {
    "provider": "codex",
    "source": "oauth",
    "usage": {
      "accountEmail": "user@example.com",
      "loginMethod": "prolite",
      "primary": null,
      "secondary": {"resetsAt": "2026-09-19T22:00:14Z", "usedPercent": 53, "windowMinutes": 10080},
      "tertiary": null,
      "updatedAt": "2026-09-17T22:01:17Z"
    }
  },
  {
    "provider": "claude",
    "usage": {
      "identity": {"providerID": "claude"},
      "primary": {"resetsAt": "2026-09-18T02:30:00Z", "usedPercent": 16.4, "windowMinutes": 300},
      "secondary": {"resetsAt": "2026-09-19T01:00:00Z", "usedPercent": 11, "windowMinutes": 10080},
      "extraRateWindows": [
        {"id": "claude-weekly-scoped-fable", "title": "Fable only",
         "window": {"resetsAt": "2026-09-19T01:00:00Z", "usedPercent": 8, "windowMinutes": 10080}}
      ],
      "updatedAt": "2026-09-17T22:01:25Z"
    }
  }
]`

// fakeCommand writes a script that prints stdout and exits with code.
func fakeCommand(t *testing.T, stdout, stderr string, code int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("シェルスクリプトを使うためUnixのみ")
	}
	path := filepath.Join(t.TempDir(), "fake-codexbar")
	script := "#!/bin/sh\ncat <<'OUT'\n" + stdout + "\nOUT\n" +
		"cat >&2 <<'ERR'\n" + stderr + "\nERR\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func Testレート制限の出力をウィンドウに変換する(t *testing.T) {
	providers, err := parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 {
		t.Fatalf("providers = %d", len(providers))
	}
	codex := providers[0]
	if codex.Provider != "codex" || codex.DisplayName != "Codex" {
		t.Errorf("codex = %+v", codex)
	}
	if codex.Plan != "prolite" || codex.Account != "user@example.com" {
		t.Errorf("plan/account = %q %q", codex.Plan, codex.Account)
	}
	if len(codex.Windows) != 1 {
		t.Fatalf("codex windows = %+v", codex.Windows)
	}
	if w := codex.Windows[0]; w.Key != "secondary" || w.Label != "7d" || w.UsedPercent != 53 {
		t.Errorf("codex window = %+v", w)
	}
	if codex.Windows[0].ResetsAt == nil || !codex.Windows[0].ResetsAt.Equal(time.Date(2026, 9, 19, 22, 0, 14, 0, time.UTC)) {
		t.Errorf("resetsAt = %v", codex.Windows[0].ResetsAt)
	}

	claude := providers[1]
	if len(claude.Windows) != 3 {
		t.Fatalf("claude windows = %+v", claude.Windows)
	}
	if w := claude.Windows[0]; w.Label != "5h" || w.UsedPercent != 16 {
		t.Errorf("5時間ウィンドウ = %+v", w)
	}
	if w := claude.Windows[2]; w.Scope != "Fable only" || w.Label != "7d" || w.UsedPercent != 8 {
		t.Errorf("モデル別ウィンドウ = %+v", w)
	}
}

func Testウィンドウ長のラベルは時間と日で表す(t *testing.T) {
	cases := map[int]string{300: "5h", 10080: "7d", 60: "1h", 90: "90m", 0: ""}
	for minutes, want := range cases {
		if got := windowLabel(minutes); got != want {
			t.Errorf("windowLabel(%d) = %q, want %q", minutes, got, want)
		}
	}
}

func Test取得したスナップショットがキャッシュされる(t *testing.T) {
	svc := New(fakeCommand(t, sample, "", 0), time.Hour)
	if got := svc.Snapshot(); len(got.Providers) != 0 {
		t.Fatalf("初期スナップショット = %+v", got)
	}
	snap := svc.Refresh(context.Background())
	if snap.Error != "" || len(snap.Providers) != 2 || snap.FetchedAt == nil {
		t.Fatalf("refresh = %+v", snap)
	}
	if cached := svc.Snapshot(); len(cached.Providers) != 2 {
		t.Errorf("キャッシュ = %+v", cached)
	}
}

func Test取得に失敗しても前回の値を保持する(t *testing.T) {
	svc := New(fakeCommand(t, sample, "", 0), time.Hour)
	svc.Refresh(context.Background())
	fetchedAt := svc.Snapshot().FetchedAt

	svc.cmd = fakeCommand(t, "", "codexbar: not logged in", 1)
	snap := svc.Refresh(context.Background())
	if snap.Error == "" {
		t.Fatal("エラーが設定されていない")
	}
	if len(snap.Providers) != 2 {
		t.Errorf("前回のプロバイダーが失われた: %+v", snap.Providers)
	}
	if snap.FetchedAt == nil || !snap.FetchedAt.Equal(*fetchedAt) {
		t.Errorf("fetchedAt = %v, want %v", snap.FetchedAt, fetchedAt)
	}
}

func Test使用状況の取得を無効にできる(t *testing.T) {
	if svc := New(Disabled, time.Hour); svc != nil {
		t.Fatal("off で無効にならない")
	}
	var svc *Service
	if snap := svc.Snapshot(); snap.Error == "" || len(snap.Providers) != 0 {
		t.Errorf("無効時のスナップショット = %+v", snap)
	}
}
