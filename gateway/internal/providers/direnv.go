package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

// DirEnv is what direnv loads for a folder, as a terminal that cd's into
// it would get: variables to set and variables to unset.
type DirEnv struct {
	Set   map[string]string
	Unset []string
}

// Empty reports whether nothing would change.
func (d DirEnv) Empty() bool { return len(d.Set) == 0 && len(d.Unset) == 0 }

// Apply returns environ with the changes made. Variables the gateway never
// passes to an agent (ChildEnv's list) are not taken from direnv either.
func (d DirEnv) Apply(environ []string) []string {
	if d.Empty() {
		return environ
	}
	drop := map[string]bool{}
	for _, k := range d.Unset {
		drop[k] = true
	}
	var out []string
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if _, ok := d.Set[k]; ok || drop[k] {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range d.Set {
		if !childEnvDrop[k] {
			out = append(out, k+"="+v)
		}
	}
	sort.Strings(out)
	return out
}

// direnvTimeout bounds one load. An .envrc that builds a Nix shell can be
// slow the first time; a session start waits for it, so it is capped.
const direnvTimeout = 15 * time.Second

// direnvBinary finds direnv. A Gateway started by the menu-bar app has a
// short PATH, so the usual install locations are tried too. It is a
// variable so tests can point it at a stand-in.
var direnvBinary = func() string {
	if p, err := exec.LookPath("direnv"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		"/opt/homebrew/bin/direnv",
		"/usr/local/bin/direnv",
		filepath.Join(home, ".nix-profile", "bin", "direnv"),
		"/run/current-system/sw/bin/direnv",
		filepath.Join(home, ".local", "bin", "direnv"),
	} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// LoadDirEnv runs `direnv export json` in dir against environ (the
// environment the agent would otherwise get) and returns what it changes.
// Only an .envrc the user has approved with `direnv allow` is loaded; that
// approval is direnv's own. Without direnv, without an .envrc or .env in dir
// or a parent, or when loading fails, nothing changes and the session starts
// as before.
func LoadDirEnv(ctx context.Context, dir string, environ []string) DirEnv {
	if dir == "" || !hasEnvrc(dir) {
		return DirEnv{}
	}
	bin := direnvBinary()
	if bin == "" {
		return DirEnv{}
	}
	ctx, cancel := context.WithTimeout(ctx, direnvTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "export", "json")
	cmd.Dir = dir
	// Without a previous direnv state it reports everything the .envrc
	// sets, not a difference from a folder the gateway happened to be in.
	var env []string
	for _, kv := range environ {
		if !strings.HasPrefix(kv, "DIRENV_") {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// An .envrc may start background processes that keep the pipes open.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		// stderr is not logged: an .envrc's own output can carry secrets.
		reason := "failed"
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			reason = "timed out"
		case strings.Contains(stderr.String(), "is blocked"):
			reason = "not allowed (run `direnv allow` in the folder)"
		}
		slog.Warn("direnv environment not loaded: "+reason, "operation", "direnv.export", "cwd", dir, "error", err)
		return DirEnv{}
	}
	d, err := parseDirEnv(stdout.Bytes())
	if err != nil {
		slog.Warn("direnv environment not loaded: unreadable output", "operation", "direnv.export", "cwd", dir, "error", err)
		return DirEnv{}
	}
	if !d.Empty() {
		names := make([]string, 0, len(d.Set))
		for k := range d.Set {
			if !strings.HasPrefix(k, "DIRENV_") {
				names = append(names, k)
			}
		}
		sort.Strings(names)
		slog.Info("direnv environment loaded", "operation", "direnv.export", "cwd", dir, "set", names, "unset", d.Unset)
	}
	return d
}

func parseDirEnv(out []byte) (DirEnv, error) {
	if len(bytes.TrimSpace(out)) == 0 {
		return DirEnv{}, nil
	}
	var raw map[string]*string
	if err := json.Unmarshal(out, &raw); err != nil {
		return DirEnv{}, err
	}
	d := DirEnv{Set: map[string]string{}}
	for k, v := range raw {
		if !envNamePattern.MatchString(k) {
			continue
		}
		if v == nil {
			d.Unset = append(d.Unset, k)
		} else {
			d.Set[k] = *v
		}
	}
	sort.Strings(d.Unset)
	return d, nil
}

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// hasEnvrc reports whether dir or a parent has a file direnv would load,
// so folders without one never start a process.
func hasEnvrc(dir string) bool {
	for _, d := range ParentDirs(dir) {
		for _, name := range []string{".envrc", ".env"} {
			if st, err := os.Stat(filepath.Join(d, name)); err == nil && !st.IsDir() {
				return true
			}
		}
	}
	return false
}

// ChildEnvIn is ChildEnv for an agent working in dir: what direnv loads
// there is added, and set still wins.
func ChildEnvIn(ctx context.Context, dir string, environ []string, set map[string]string) []string {
	env := ChildEnv(environ, set)
	d := LoadDirEnv(ctx, dir, env)
	if d.Empty() {
		return env
	}
	return ChildEnv(d.Apply(env), set)
}
