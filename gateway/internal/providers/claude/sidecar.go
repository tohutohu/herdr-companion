package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func afterSeconds(n int) <-chan time.Time { return time.After(time.Duration(n) * time.Second) }

// sidecar is the herdr-claude-sidecar child (gateway/claude-sidecar), which
// runs Claude Code sessions through the Claude Agent SDK. The protocol is one
// JSON object per line over stdio: requests with an id, their responses, and
// events about sessions. It only listens on its own pipes.
type sidecar struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	wmu     sync.Mutex
	nextID  atomic.Int64
	mu      sync.Mutex
	waiting map[int64]chan sidecarReply
	done    chan struct{}
	err     error
}

type sidecarReply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type sidecarEvent struct {
	Event     string          `json:"event"`
	SessionID string          `json:"sessionId"`
	Data      json.RawMessage `json:"data"`
}

// sidecarError is an error the sidecar reported for a request: the request
// was received and refused, as opposed to a broken connection.
type sidecarError struct{ msg string }

func (e *sidecarError) Error() string { return "claude sidecar: " + e.msg }

func startSidecar(bin string, onEvent func(sidecarEvent), onExit func(error)) (*sidecar, error) {
	cmd := exec.Command(bin)
	cmd.Env = sidecarEnv(os.Environ())
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", bin, err)
	}
	s := &sidecar{cmd: cmd, stdin: stdin, waiting: map[int64]chan sidecarReply{}, done: make(chan struct{})}
	go logSidecarStderr(stderr)
	go s.loop(stdout, onEvent, onExit)
	slog.Info("started claude sidecar", "provider", providerName, "operation", "sidecar.start", "pid", cmd.Process.Pid, "path", bin)
	return s, nil
}

// sidecarEnv keeps secrets out of the sidecar's own logs; the per-session
// environment for Claude Code is passed with each start.
func sidecarEnv(environ []string) []string {
	var out []string
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if k == "ANTHROPIC_API_KEY" || k == "ANTHROPIC_AUTH_TOKEN" {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// logSidecarStderr logs the sidecar's diagnostics at debug level, cut short:
// Claude Code's stderr may quote anything.
func logSidecarStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if len(line) > 500 {
			line = line[:500] + "…"
		}
		slog.Debug("claude sidecar", "provider", providerName, "operation", "sidecar.stderr", "line", line)
	}
}

func (s *sidecar) loop(stdout io.Reader, onEvent func(sidecarEvent), onExit func(error)) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var head struct {
			ID    *int64 `json:"id"`
			Event string `json:"event"`
		}
		line := sc.Bytes()
		if json.Unmarshal(line, &head) != nil {
			continue
		}
		if head.Event != "" {
			var ev sidecarEvent
			if json.Unmarshal(line, &ev) == nil {
				onEvent(ev)
			}
			continue
		}
		if head.ID == nil {
			continue
		}
		var r sidecarReply
		json.Unmarshal(line, &r)
		s.mu.Lock()
		ch := s.waiting[*head.ID]
		delete(s.waiting, *head.ID)
		s.mu.Unlock()
		if ch != nil {
			ch <- r
		}
	}
	err := sc.Err()
	waitErr := s.cmd.Wait()
	if err == nil {
		err = waitErr
	}
	if err == nil {
		err = errors.New("claude sidecar exited")
	}
	s.mu.Lock()
	s.err = err
	for _, ch := range s.waiting {
		close(ch)
	}
	s.waiting = map[int64]chan sidecarReply{}
	s.mu.Unlock()
	close(s.done)
	onExit(err)
}

func (s *sidecar) alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

func (s *sidecar) call(ctx context.Context, method string, params, out any) error {
	id := s.nextID.Add(1)
	ch := make(chan sidecarReply, 1)
	s.mu.Lock()
	if !s.alive() {
		s.mu.Unlock()
		return fmt.Errorf("claude sidecar is not running: %v", s.err)
	}
	s.waiting[id] = ch
	s.mu.Unlock()
	b, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	s.wmu.Lock()
	_, err = s.stdin.Write(append(b, '\n'))
	s.wmu.Unlock()
	if err != nil {
		s.mu.Lock()
		delete(s.waiting, id)
		s.mu.Unlock()
		return err
	}
	select {
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.waiting, id)
		s.mu.Unlock()
		return ctx.Err()
	case r, ok := <-ch:
		if !ok {
			return errors.New("claude sidecar exited")
		}
		if r.Error != nil {
			return &sidecarError{r.Error.Message}
		}
		if out != nil {
			return json.Unmarshal(r.Result, out)
		}
		return nil
	}
}

// close ends the sidecar: closing stdin makes it close every session and
// exit; a sidecar that does not is killed.
func (s *sidecar) close() {
	s.stdin.Close()
	select {
	case <-s.done:
	case <-afterSeconds(5):
		if s.cmd.Process != nil {
			s.cmd.Process.Kill()
		}
	}
}
