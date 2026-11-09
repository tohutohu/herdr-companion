package resident

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// Precheck is a shell command the gateway runs in the task's folder before
// an automatic run starts the agent, so a cheap check (a scrape, an API
// call, a file comparison) decides whether the agent is needed at all.
//
// Exit 0 starts the agent with the command's output in its prompt; exit 1
// ends the run without the agent (recorded as skipped); anything else, a
// timeout or a command that cannot start fails the run. Runs the user asked
// for (manual, follow-up, answer) always start the agent: the check's
// result is then context only.
//
// The command runs with the user's privileges, outside the agent's
// permission profile and sandbox.
type Precheck struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
}

const (
	DefaultPrecheckTimeoutSeconds = 60
	maxPrecheckTimeoutSeconds     = 600
	maxPrecheckCommandBytes       = 4 << 10
	// maxPrecheckOutputBytes of stdout reach the prompt.
	maxPrecheckOutputBytes = 32 << 10
	maxPrecheckStderrBytes = 4 << 10
)

// normalizePrecheck checks a precheck; an empty command removes it.
func normalizePrecheck(p *Precheck, maxRunMinutes int) (*Precheck, error) {
	if p == nil || strings.TrimSpace(p.Command) == "" {
		return nil, nil
	}
	out := *p
	out.Command = strings.TrimSpace(out.Command)
	if len(out.Command) > maxPrecheckCommandBytes {
		return nil, invalid("precheck command is longer than 4 KiB")
	}
	if strings.ContainsRune(out.Command, 0) {
		return nil, invalid("precheck command contains a NUL byte")
	}
	if out.TimeoutSeconds == 0 {
		out.TimeoutSeconds = DefaultPrecheckTimeoutSeconds
	}
	if out.TimeoutSeconds < 1 || out.TimeoutSeconds > maxPrecheckTimeoutSeconds {
		return nil, invalid("precheck timeoutSeconds must be between 1 and %d", maxPrecheckTimeoutSeconds)
	}
	if out.TimeoutSeconds >= maxRunMinutes*60 {
		return nil, invalid("precheck timeoutSeconds must be shorter than maxRunMinutes")
	}
	return &out, nil
}

// PrecheckResult is what a precheck command did.
type PrecheckResult struct {
	Command  string
	ExitCode int
	// Output is the command's stdout, cut at maxPrecheckOutputBytes.
	Output    string
	Truncated bool
	Stderr    string
	Duration  time.Duration
}

// Matched means the command asked for the agent (exit 0).
func (r *PrecheckResult) Matched() bool { return r.ExitCode == 0 }

// errPrecheck marks a precheck that failed (neither exit 0 nor exit 1).
var errPrecheck = errors.New("precheck failed")

// PrecheckRunner runs a task's precheck command. Tests replace it.
type PrecheckRunner func(ctx context.Context, req PrecheckRequest) (*PrecheckResult, error)

// PrecheckRequest is one precheck invocation.
type PrecheckRequest struct {
	TaskID   string
	RunID    string
	Cwd      string
	Triggers []string
	Precheck Precheck
	// Memory is written to the command's stdin as JSON, so a script can
	// compare with what earlier runs recorded.
	Memory *Memory
}

// RunPrecheck runs the command with /bin/sh -c in its own process group,
// which is killed when the command ends, times out or the run stops.
func RunPrecheck(ctx context.Context, req PrecheckRequest) (*PrecheckResult, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, time.Duration(req.Precheck.TimeoutSeconds)*time.Second, errPrecheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", req.Precheck.Command)
	cmd.Dir = req.Cwd
	cmd.Env = providers.ChildEnvIn(ctx, req.Cwd, os.Environ(), map[string]string{
		"HC_RESIDENT":          "1",
		"HC_RESIDENT_TASK":     req.TaskID,
		"HC_RESIDENT_RUN":      req.RunID,
		"HC_RESIDENT_TRIGGERS": strings.Join(req.Triggers, ","),
	})
	mem := req.Memory
	if mem == nil {
		mem = &Memory{State: map[string]json.RawMessage{}}
	}
	stdin, _ := json.Marshal(map[string]any{"summary": mem.Summary, "state": nonNilState(mem.State)})
	cmd.Stdin = bytes.NewReader(stdin)
	stdout := &capWriter{max: maxPrecheckOutputBytes}
	stderr := &capWriter{max: maxPrecheckStderrBytes, tail: true}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	// Background children holding the pipes must not keep the run waiting.
	cmd.WaitDelay = 2 * time.Second
	start := time.Now()
	err := cmd.Run()
	if cmd.Process != nil {
		// Whatever the command left running in its group ends with it.
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	res := &PrecheckResult{Command: req.Precheck.Command, Output: stdout.String(), Truncated: stdout.cut(),
		Stderr: strings.TrimSpace(stderr.String()), Duration: time.Since(start), ExitCode: -1}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if cause := context.Cause(ctx); cause != nil {
		if errors.Is(cause, errPrecheckTimeout) {
			return res, fmt.Errorf("%w: no exit within %d seconds", errPrecheck, req.Precheck.TimeoutSeconds)
		}
		return res, cause
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) && !errors.Is(err, exec.ErrWaitDelay) {
		return res, fmt.Errorf("%w: %v", errPrecheck, err)
	}
	if res.ExitCode != 0 && res.ExitCode != 1 {
		msg := fmt.Sprintf("exit %d", res.ExitCode)
		if res.ExitCode == -1 {
			msg = "killed by a signal"
		}
		if res.Stderr != "" {
			msg += ": " + res.Stderr
		}
		return res, fmt.Errorf("%w: %s", errPrecheck, msg)
	}
	return res, nil
}

var errPrecheckTimeout = errors.New("precheck timed out")

func nonNilState(m map[string]json.RawMessage) map[string]json.RawMessage {
	if m == nil {
		return map[string]json.RawMessage{}
	}
	return m
}

// capWriter keeps the first (or, with tail, the last) max bytes and
// discards the rest, so a chatty command never blocks on a full pipe.
type capWriter struct {
	buf   []byte
	max   int
	tail  bool
	total int
}

func (w *capWriter) Write(p []byte) (int, error) {
	w.total += len(p)
	if w.tail {
		w.buf = append(w.buf, p...)
		if len(w.buf) > w.max {
			w.buf = append([]byte(nil), w.buf[len(w.buf)-w.max:]...)
		}
		return len(p), nil
	}
	if room := w.max - len(w.buf); room > 0 {
		w.buf = append(w.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (w *capWriter) cut() bool { return w.total > w.max }

func (w *capWriter) String() string {
	return strings.ToValidUTF8(string(w.buf), "")
}
