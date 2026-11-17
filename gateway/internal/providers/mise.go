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
	"sort"
	"strings"
	"syscall"
	"time"
)

// miseTimeout bounds one `mise env`. It does not install anything, so it
// is quick; the bound only guards against a hang.
const miseTimeout = 15 * time.Second

// miseBinary finds mise. It is a variable so tests can point it at a
// stand-in.
var miseBinary = func() string {
	if p, err := exec.LookPath("mise"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		filepath.Join(home, ".local", "bin", "mise"),
		"/opt/homebrew/bin/mise",
		"/usr/local/bin/mise",
		filepath.Join(home, ".local", "share", "mise", "bin", "mise"),
		"/usr/bin/mise",
	} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

// miseConfigFiles are the files that make a folder (or a parent) select
// mise tools or variables.
var miseConfigFiles = []string{
	"mise.toml", ".mise.toml", "mise.local.toml", ".mise.local.toml",
	".tool-versions",
	filepath.Join(".mise", "config.toml"), filepath.Join("mise", "config.toml"),
	filepath.Join(".config", "mise.toml"), filepath.Join(".config", "mise", "config.toml"),
}

// hasMiseConfig reports whether dir or a parent has a mise config file, so
// folders without one never start a process. The global config is in the
// home folder, which this also finds for folders inside it.
func hasMiseConfig(dir string) bool {
	for _, d := range ParentDirs(dir) {
		for _, name := range miseConfigFiles {
			if st, err := os.Stat(filepath.Join(d, name)); err == nil && !st.IsDir() {
				return true
			}
		}
	}
	return false
}

// LoadMiseEnv runs `mise env --json` in dir against environ and returns
// the tools' PATH and the variables mise sets there, as a terminal with
// `mise activate` that cd's into it would get. A config file the user has
// not trusted with `mise trust` is not loaded (mise refuses it). Without
// mise, without a config file, or when loading fails, nothing changes.
func LoadMiseEnv(ctx context.Context, dir string, environ []string) DirEnv {
	if dir == "" || !hasMiseConfig(dir) {
		return DirEnv{}
	}
	bin := miseBinary()
	if bin == "" {
		return DirEnv{}
	}
	ctx, cancel := context.WithTimeout(ctx, miseTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "env", "--json")
	cmd.Dir = dir
	// Without an activated shell's state mise reports the folder's whole
	// environment, not a difference from where the gateway started.
	var env []string
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(k, "__MISE_") && k != "MISE_SHELL" {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		// stderr is not logged: it can echo the config's values.
		reason := "failed"
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			reason = "timed out"
		case strings.Contains(stderr.String(), "not trusted"):
			reason = "not trusted (run `mise trust` in the folder)"
		}
		slog.Warn("mise environment not loaded: "+reason, "operation", "mise.env", "cwd", dir, "error", err)
		return DirEnv{}
	}
	d, err := parseMiseEnv(stdout.Bytes(), envValue(environ, "PATH"))
	if err != nil {
		slog.Warn("mise environment not loaded: unreadable output", "operation", "mise.env", "cwd", dir, "error", err)
		return DirEnv{}
	}
	if !d.Empty() {
		names := make([]string, 0, len(d.Set))
		for k := range d.Set {
			names = append(names, k)
		}
		sort.Strings(names)
		slog.Info("mise environment loaded", "operation", "mise.env", "cwd", dir, "set", names)
	}
	return d
}

// parseMiseEnv reads `mise env --json`. Its PATH is the tools' folders in
// front of the PATH it was given; repeated entries are dropped, and the
// value is left out when it would not change anything.
func parseMiseEnv(out []byte, path string) (DirEnv, error) {
	if len(bytes.TrimSpace(out)) == 0 {
		return DirEnv{}, nil
	}
	var raw map[string]string
	if err := json.Unmarshal(out, &raw); err != nil {
		return DirEnv{}, err
	}
	d := DirEnv{Set: map[string]string{}}
	for k, v := range raw {
		if !envNamePattern.MatchString(k) || childEnvDrop[k] || strings.HasPrefix(k, "__MISE_") {
			continue
		}
		if k == "PATH" {
			v = joinPath(v)
			if v == joinPath(path) {
				continue
			}
		}
		d.Set[k] = v
	}
	return d, nil
}

func envValue(environ []string, key string) string {
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

func joinPath(lists ...string) string {
	seen := map[string]bool{}
	var out []string
	for _, l := range lists {
		for _, e := range filepath.SplitList(l) {
			if e != "" && !seen[e] {
				seen[e] = true
				out = append(out, e)
			}
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}
