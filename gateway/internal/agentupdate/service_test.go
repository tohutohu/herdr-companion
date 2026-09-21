package agentupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func Test更新は重複せずHTTP切断後も結果を取得できる(t *testing.T) {
	s := New(context.Background(), "my-codex", "")
	gate := make(chan struct{})
	var calls atomic.Int32
	s.run = func(_ context.Context, command string, args ...string) (string, error) {
		if args[0] == "--version" {
			return "1.2.3", nil
		}
		if command != "my-codex" || strings.Join(args, " ") != "update" {
			t.Errorf("unexpected command: %s %v", command, args)
		}
		calls.Add(1)
		<-gate
		return "Updated", nil
	}
	if _, err := s.Start("codex; echo unsafe"); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
	first, _ := s.Start("codex")
	second, _ := s.Start("codex")
	if first.State != "running" || second.State != "running" {
		t.Fatal(first, second)
	}
	close(gate)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		st := s.List(context.Background())[1]
		if st.State != "running" {
			if st.State != "succeeded" || st.Version != "1.2.3" || st.Output != "Updated" || calls.Load() != 1 {
				t.Fatal(st, calls.Load())
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("update did not complete")
}
func Test更新失敗はバージョンとエラーを残す(t *testing.T) {
	s := New(context.Background(), "codex", "")
	s.run = func(_ context.Context, _ string, args ...string) (string, error) {
		if args[0] == "--version" {
			return "old", nil
		}
		return "network unavailable", errors.New("exit 1")
	}
	s.Start("claude")
	for range 1000 {
		st := s.List(context.Background())[0]
		if st.State == "failed" {
			if st.Version != "old" || st.Error == "" || st.Output != "network unavailable" {
				t.Fatal(st)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("failure not reported")
}
func Test出力の末尾だけ保持する(t *testing.T) {
	var b boundedOutput
	b.Write([]byte(strings.Repeat("a", 40000)))
	b.Write([]byte("end"))
	if len(b.text) != 32768 || !strings.HasSuffix(string(b.text), "end") {
		t.Fatal(len(b.text))
	}
}

func Testタイムアウトで更新コマンドと子プロセスを止める(t *testing.T) {
	path := filepath.Join(t.TempDir(), "updater")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n/bin/sleep 60 &\nwait\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := runCommand(ctx, path, "update")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatal(err, time.Since(start))
	}
}

func TestOpenCodeは設定済みバイナリのupgradeを使い更新結果を検証する(t *testing.T) {
	for _, version := range []string{"1.2.0", "opencode v2.0.9"} {
		t.Run(version, func(t *testing.T) {
			s := New(context.Background(), "", "/custom/bin/opencode")
			gate := make(chan struct{})
			var updates atomic.Int32
			s.run = func(_ context.Context, command string, args ...string) (string, error) {
				if args[0] == "--version" {
					return version, nil
				}
				if command != "/custom/bin/opencode" || strings.Join(args, " ") != "upgrade" {
					t.Errorf("unexpected command: %s %v", command, args)
				}
				updates.Add(1)
				<-gate
				return "Upgrade complete", nil
			}
			list := s.List(context.Background())
			if len(list) != 4 || list[2].Provider != "opencode" || list[2].Version != version {
				t.Fatal(list)
			}
			first, err := s.Start("opencode")
			if err != nil || first.State != "running" {
				t.Fatal(first, err)
			}
			second, err := s.Start("opencode")
			if err != nil || second.State != "running" {
				t.Fatal(second, err)
			}
			close(gate)
			for range 1000 {
				st := s.List(context.Background())[2]
				if st.State != "running" {
					if st.State != "succeeded" || st.Version != version || st.Output != "Upgrade complete" || updates.Load() != 1 {
						t.Fatal(st, updates.Load())
					}
					return
				}
				time.Sleep(time.Millisecond)
			}
			t.Fatal("update did not complete")
		})
	}
}
func TestOpenCodeの未導入と更新後バージョン確認失敗を表示する(t *testing.T) {
	s := New(context.Background(), "", "")
	s.run = func(_ context.Context, command string, args ...string) (string, error) {
		if command == "opencode" && args[0] == "--version" {
			return "", errors.New("binary unavailable")
		}
		return "done", nil
	}
	if st := s.List(context.Background())[2]; st.Provider != "opencode" || st.State != "idle" || st.Error == "" {
		t.Fatal(st)
	}
	s.Start("opencode")
	for range 1000 {
		st := s.List(context.Background())[2]
		if st.State == "failed" {
			if !strings.Contains(st.Error, "version verification failed") {
				t.Fatal(st)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("verification failure not reported")
}
