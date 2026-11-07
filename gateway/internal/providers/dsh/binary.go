package dsh

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// FindBinary returns the dsh executable to run, or "" when none is found.
// A configured path wins. A Gateway started by the menu-bar app has a short
// PATH without Node version managers, so the places npm puts global
// commands for them are searched too, newest Node first.
func FindBinary(configured string) string {
	if configured != "" {
		return configured
	}
	if p, err := exec.LookPath("dsh"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range candidateBinaries(home) {
		if executable(p) {
			return p
		}
	}
	return ""
}

func candidateBinaries(home string) []string {
	var out []string
	// Version-manager installs keep node next to the global command, which
	// ChildPath puts first on the child's PATH.
	for _, root := range []string{
		filepath.Join(home, ".local", "share", "mise", "installs", "node"),
		filepath.Join(home, ".nvm", "versions", "node"),
		filepath.Join(home, ".local", "share", "fnm", "node-versions"),
		filepath.Join(home, ".asdf", "installs", "nodejs"),
	} {
		for _, version := range newestFirst(root) {
			out = append(out,
				filepath.Join(root, version, "bin", "dsh"),
				filepath.Join(root, version, "installation", "bin", "dsh"))
		}
	}
	return append(out,
		filepath.Join(home, ".volta", "bin", "dsh"),
		filepath.Join(home, ".local", "share", "mise", "shims", "dsh"),
		filepath.Join(home, ".asdf", "shims", "dsh"),
		filepath.Join(home, "Library", "pnpm", "dsh"),
		filepath.Join(home, ".bun", "bin", "dsh"),
		filepath.Join(home, ".local", "bin", "dsh"),
		filepath.Join(home, ".npm-global", "bin", "dsh"),
		"/opt/homebrew/bin/dsh",
		"/usr/local/bin/dsh",
	)
}

// newestFirst lists the version directories under root, highest version
// first. Aliases such as "latest" or "lts" sort after real versions.
func newestFirst(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.SliceStable(names, func(i, j int) bool { return newer(names[i], names[j]) })
	return names
}

// newer orders version directories: higher versions first, then aliases by name.
func newer(a, b string) bool {
	pa, pb := versionParts(a), versionParts(b)
	if pa == nil || pb == nil {
		if (pa == nil) != (pb == nil) {
			return pb == nil
		}
		return a < b
	}
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x > y
		}
	}
	return len(pa) > len(pb)
}

func versionParts(name string) []int {
	var out []int
	for _, part := range strings.Split(strings.TrimPrefix(name, "v"), ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

func executable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0111 != 0
}

// ChildPath puts the directory of an absolute dsh first on PATH: its
// "#!/usr/bin/env node" then finds the Node it was installed with.
func ChildPath(binary, path string) string {
	if !filepath.IsAbs(binary) {
		return path
	}
	dir := filepath.Dir(binary)
	if path == "" {
		return dir
	}
	return dir + string(os.PathListSeparator) + path
}
