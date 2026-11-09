package dsh

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

const callTimeout = 30 * time.Second
const maxFrame = 32 << 20

type frame struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("DSH RPC %d: %s", e.Code, e.Message) }

// One stdio connection owns exactly one root session. The SDK has no cancel
// method, so an interrupt closes only this process, preserving native history.
type conn struct {
	tag       string
	cmd       *exec.Cmd
	in        io.WriteCloser
	out       io.ReadCloser
	mu        sync.Mutex
	writeMu   sync.Mutex
	next      int
	waiters   map[string]chan frame
	done      chan struct{}
	closeOnce sync.Once
}

type launchSpec struct {
	binary, home, cwd, mode string
	unattended              bool
	// env is added to the child's environment; it carries API keys.
	env   map[string]string
	patch string
}

func (p *Provider) launchSpec(cwd, mode string) launchSpec {
	env := map[string]string{}
	if p.cfg.APIKey != "" {
		env["DEEPSEEK_API_KEY"] = p.cfg.APIKey
	}
	for route, key := range p.cfg.ProviderKeys {
		env[keyEnv(route)] = key
	}
	return launchSpec{binary: p.cfg.Binary, home: p.cfg.Home, cwd: cwd, mode: mode, unattended: p.unattended != nil, env: env, patch: p.patch}
}

func launch(spec launchSpec) (*conn, error) {
	args := []string{"--profile", "sdk"}
	if spec.patch != "" {
		args = append(args, "--patch", spec.patch)
	}
	cmd := exec.Command(spec.binary, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Dir = spec.cwd
	// What direnv loads for the folder comes first, so dsh's own PATH
	// entry is put in front of the folder's PATH rather than replacing it.
	environ := os.Environ()
	environ = providers.LoadDirEnv(context.Background(), spec.cwd, providers.ChildEnv(environ, nil)).Apply(environ)
	set := map[string]string{"DSH_HOME": spec.home, "DSH_PERMISSION_MODE": spec.mode, "PATH": ChildPath(spec.binary, envValue(environ, "PATH"))}
	if spec.unattended {
		set["HC_RESIDENT"] = "1"
	}
	for k, v := range spec.env {
		set[k] = v
	}
	cmd.Env = providers.ChildEnv(environ, set)
	// Runtime diagnostics can contain credentials. Protocol failures are
	// reported through RPC; never copy arbitrary stderr into gateway logs.
	cmd.Stderr = io.Discard
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		in.Close()
		out.Close()
		return nil, fmt.Errorf("%w: install DSH or set dshBinary: %v", providers.ErrRuntimeUnavailable, err)
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, err
	}
	return &conn{tag: hex.EncodeToString(nonce[:]), cmd: cmd, in: in, out: out, waiters: map[string]chan frame{}, done: make(chan struct{})}, nil
}

func (c *conn) read(onFrame func(frame), onExit func(error)) {
	scanner := bufio.NewScanner(c.out)
	scanner.Buffer(make([]byte, 64<<10), maxFrame)
	for scanner.Scan() {
		var f frame
		if err := json.Unmarshal(scanner.Bytes(), &f); err != nil {
			// A corrupt channel is unsafe to keep using for approvals.
			c.kill()
			break
		}
		if f.Method != "" {
			onFrame(f)
			continue
		}
		c.mu.Lock()
		ch := c.waiters[string(f.ID)]
		delete(c.waiters, string(f.ID))
		c.mu.Unlock()
		if ch != nil {
			ch <- f
		}
	}
	if scanner.Err() != nil {
		c.kill()
	}
	err := c.cmd.Wait()
	if err == nil {
		err = errors.New("DSH runtime exited")
	}
	onExit(err)
	close(c.done)
}

func (c *conn) write(value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(b) >= maxFrame {
		return errors.New("DSH request exceeds 32 MiB")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.in.Write(append(b, '\n'))
	return err
}

func (c *conn) call(ctx context.Context, method string, params any, result any) error {
	// A resident wait owns the task's deadline, which can be hours. Control
	// RPCs retain their short deadline; cancellation still abandons the waiter.
	cancel := func() {}
	if method != "session/wait" {
		ctx, cancel = context.WithTimeout(ctx, callTimeout)
	}
	defer cancel()
	c.mu.Lock()
	c.next++
	id := c.next
	key := fmt.Sprint(id)
	ch := make(chan frame, 1)
	c.waiters[key] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.waiters, key); c.mu.Unlock() }()
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}
	select {
	case f := <-ch:
		if f.Error != nil {
			return f.Error
		}
		if result != nil {
			return json.Unmarshal(f.Result, result)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return errors.New("DSH runtime disconnected")
	}
}

func (c *conn) reply(id json.RawMessage, result any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (c *conn) kill() {
	// The runtime owns a new process group; a wedged shutdown must not leave
	// its ordinary child commands alive or touch another session's process.
	_ = syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
}

func (c *conn) close() {
	c.closeOnce.Do(func() {
		// EOF invokes the profile's bounded disposal and flushes persistence.
		c.in.Close()
		select {
		case <-c.done:
		case <-time.After(5 * time.Second):
			c.kill()
			<-c.done
		}
	})
}

func envValue(environ []string, key string) string {
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}
