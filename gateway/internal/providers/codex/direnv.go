package codex

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// dirEnvConfig returns thread config overrides that give the commands the
// agent runs in cwd what direnv loads there. Every thread shares one
// app-server process, so the variables go through
// shell_environment_policy.set, one key each so the user's own set entries
// stay. Variables direnv unsets cannot be expressed there and are left.
func dirEnvConfig(ctx context.Context, c *rpcClient, cwd string) map[string]any {
	base := providers.ChildEnv(os.Environ(), nil)
	d := providers.LoadDirEnv(ctx, cwd, base)
	if len(d.Set) == 0 {
		return nil
	}
	cfg := map[string]any{}
	for k, v := range d.Set {
		if k == "PATH" {
			continue
		}
		cfg["shell_environment_policy.set."+k] = v
	}
	if path, ok := d.Set["PATH"]; ok && path != os.Getenv("PATH") {
		// The app-server puts its own folders (apply_patch, rg) in front
		// of the PATH it was given; replacing PATH must keep them.
		server, err := serverPath(ctx, c)
		if err != nil {
			slog.Warn("direnv PATH not passed to Codex: could not read the app-server's PATH", "provider", providerName, "operation", "direnv.path", "cwd", cwd, "error", err)
		} else {
			cfg["shell_environment_policy.set.PATH"] = mergePath(server, os.Getenv("PATH"), path)
		}
	}
	return cfg
}

func serverPath(ctx context.Context, c *rpcClient) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var r struct {
		ExitCode int    `json:"exitCode"`
		Stdout   string `json:"stdout"`
	}
	params := map[string]any{"command": []string{"/usr/bin/printenv", "PATH"}, "timeoutMs": 5000}
	if err := c.call(ctx, "command/exec", params, &r); err != nil {
		return "", err
	}
	if r.ExitCode != 0 {
		return "", fmt.Errorf("printenv exited with %d", r.ExitCode)
	}
	return strings.TrimSpace(r.Stdout), nil
}

// mergePath is direnv's PATH (computed from the gateway's) with the
// folders the app-server added to the gateway's PATH kept in front.
func mergePath(server, gateway, direnv string) string {
	known := map[string]bool{}
	for _, e := range filepath.SplitList(gateway) {
		known[e] = true
	}
	var out []string
	seen := map[string]bool{}
	add := func(e string) {
		if e != "" && !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	for _, e := range filepath.SplitList(server) {
		if !known[e] {
			add(e)
		}
	}
	for _, e := range filepath.SplitList(direnv) {
		add(e)
	}
	return strings.Join(out, string(os.PathListSeparator))
}
