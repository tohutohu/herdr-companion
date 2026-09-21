// Package agentupdate runs explicit CLI updates independently of HTTP requests.
package agentupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

var ErrUnknown = errors.New("unknown agent")

type Status struct {
	Provider string `json:"provider"`
	Version  string `json:"version"`
	State    string `json:"state"` // idle, running, succeeded, failed
	Output   string `json:"output,omitempty"`
	Error    string `json:"error,omitempty"`
}

type Service struct {
	ctx      context.Context
	mu       sync.Mutex
	commands map[string]string
	statuses map[string]Status
	run      func(context.Context, string, ...string) (string, error)
}

func New(ctx context.Context, codex, opencode string, devinArgs ...string) *Service {
	if codex == "" {
		codex = "codex"
	}
	if opencode == "" {
		opencode = "opencode"
	}
	devin := "devin"
	if len(devinArgs) > 0 && devinArgs[0] != "" {
		devin = devinArgs[0]
	}
	return &Service{ctx: ctx, commands: map[string]string{"codex": codex, "claude": "claude", "opencode": opencode, "devin": devin}, statuses: map[string]Status{}, run: runCommand}
}

// boundedOutput keeps verbose or broken installers from exhausting memory.
type boundedOutput struct{ text []byte }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	const limit = 32 * 1024
	if n >= limit {
		b.text = append(b.text[:0], p[n-limit:]...)
	} else {
		if len(b.text)+n > limit {
			b.text = b.text[len(b.text)+n-limit:]
		}
		b.text = append(b.text, p...)
	}
	return n, nil
}
func runCommand(ctx context.Context, binary string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.WaitDelay = 2 * time.Second
	// Only this updater's process group is cancelled; agent sessions are separate.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.Dir, _ = os.UserHomeDir()
	// No shell, no caller-supplied command or arguments, and no inherited stdin.
	var out boundedOutput
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return strings.TrimSpace(string(out.text)), err
}

func (s *Service) List(ctx context.Context) []Status {
	out := make([]Status, 0, 4)
	for _, id := range []string{"claude", "codex", "opencode", "devin"} {
		s.mu.Lock()
		st, ok := s.statuses[id]
		s.mu.Unlock()
		if !ok || st.State != "running" {
			c, cancel := context.WithTimeout(ctx, 5*time.Second)
			version, err := s.run(c, s.commands[id], "--version")
			cancel()
			s.mu.Lock()
			st, ok = s.statuses[id]
			if !ok {
				st = Status{Provider: id, State: "idle"}
			}
			if st.State != "running" {
				if err == nil {
					st.Version = version
				}
				if st.State == "idle" {
					st.Error = ""
					if err != nil {
						st.Error = "Could not read version: " + err.Error()
					}
				}
				s.statuses[id] = st
			}
			s.mu.Unlock()
		}
		out = append(out, st)
	}
	return out
}

// Start is idempotent while this agent is updating. Closing the app does not
// cancel the update; the gateway lifetime and a ten-minute limit bound it.
func (s *Service) Start(id string) (Status, error) {
	command, ok := s.commands[id]
	if !ok {
		return Status{}, ErrUnknown
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.statuses[id]
	if st.State == "running" {
		return st, nil
	}
	st.Provider, st.State, st.Output, st.Error = id, "running", "", ""
	s.statuses[id] = st
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Minute)
		defer cancel()
		// Both OpenCode v1 and official v2 use upgrade; the CLI detects
		// its own supported installation method (curl, npm, ...).
		verb := "update"
		if id == "opencode" {
			verb = "upgrade"
		}
		output, err := s.run(ctx, command, verb)
		next := Status{Provider: id, Version: st.Version, State: "succeeded", Output: output}
		if err != nil {
			next.State, next.Error = "failed", fmt.Sprintf("Update failed: %v", err)
		}
		vctx, vcancel := context.WithTimeout(s.ctx, 5*time.Second)
		version, verr := s.run(vctx, command, "--version")
		vcancel()
		if verr == nil {
			next.Version = version
		} else if err == nil {
			next.State, next.Error = "failed", "Update finished, but version verification failed: "+verr.Error()
		}
		s.mu.Lock()
		s.statuses[id] = next
		s.mu.Unlock()
	}()
	return st, nil
}
