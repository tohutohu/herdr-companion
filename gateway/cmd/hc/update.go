package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// latestReleaseAPI is the newest published release (GitHub leaves drafts and
// prereleases out of /latest). The release workflow attaches hc for every
// platform as hc-<os>-<arch>[.exe] with a .sha256 next to it.
const latestReleaseAPI = "https://api.github.com/repos/tohutohu/herdr-companion/releases/latest"

// maxBinarySize bounds a download so a wrong URL can't fill the disk.
const maxBinarySize = 256 << 20

// updater replaces the running hc with the binary of the newest release.
// Tests swap the release API, the executable path and the platform.
type updater struct {
	api        string
	http       *http.Client
	executable func() (string, error)
	goos       string
	goarch     string
}

func defaultUpdater() *updater {
	return &updater{
		api:        latestReleaseAPI,
		http:       &http.Client{Timeout: 5 * time.Minute},
		executable: os.Executable,
		goos:       runtime.GOOS,
		goarch:     runtime.GOARCH,
	}
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	HTMLURL string        `json:"html_url"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
	// Digest is GitHub's own "sha256:<hex>" of the uploaded file, when it
	// reports one.
	Digest string `json:"digest"`
}

type updateResult struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Updated         bool   `json:"updated"`
	Path            string `json:"path,omitempty"`
}

func (a *app) cmdUpdate(args []string, g globalFlags) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	check := fs.Bool("check", false, "")
	force := fs.Bool("force", false, "")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usagef("update takes no arguments")
	}
	u := a.updater
	if u == nil {
		u = defaultUpdater()
	}
	print := func(r updateResult, text string) error {
		if g.json {
			return json.NewEncoder(a.out).Encode(r)
		}
		fmt.Fprintln(a.out, text)
		return nil
	}

	rel, err := u.latest()
	if err != nil {
		return err
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	res := updateResult{Current: version, Latest: latest}
	cmp, comparable := compareVersions(version, latest)
	res.UpdateAvailable = !comparable || cmp < 0

	switch {
	case comparable && cmp >= 0 && !*force:
		return print(res, fmt.Sprintf("hc %s is up to date (latest release %s)", version, latest))
	case *check:
		if !comparable {
			return print(res, fmt.Sprintf("hc %s was built from source; the latest release is %s (hc update --force installs it)", version, latest))
		}
		return print(res, fmt.Sprintf("hc %s → %s is available: run \"hc update\"", version, latest))
	case !comparable && !*force:
		return fmt.Errorf("hc %s was built from source; run \"hc update --force\" to replace it with release %s", version, latest)
	}

	path, err := u.install(rel, latest)
	if err != nil {
		return err
	}
	res.Updated, res.Path = true, path
	return print(res, fmt.Sprintf("updated hc %s → %s (%s)", version, latest, path))
}

func (u *updater) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "hc/"+version)
	resp, err := u.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s %s", url, resp.Status, strings.TrimSpace(string(body)))
	}
	return resp, nil
}

func (u *updater) latest() (githubRelease, error) {
	var rel githubRelease
	resp, err := u.get(context.Background(), u.api)
	if err != nil {
		return rel, fmt.Errorf("checking the latest release: %w", err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return rel, fmt.Errorf("reading the latest release: %w", err)
	}
	if _, ok := versionParts(strings.TrimPrefix(rel.TagName, "v")); !ok {
		return rel, fmt.Errorf("the latest release has an unexpected tag %q", rel.TagName)
	}
	return rel, nil
}

func (u *updater) assetName() string {
	name := "hc-" + u.goos + "-" + u.goarch
	if u.goos == "windows" {
		name += ".exe"
	}
	return name
}

// install downloads this platform's hc from rel, checks its SHA-256 and that
// it runs and reports want, then moves it over the running executable. The
// new file gets a new inode, so a running hc keeps its own copy and macOS
// doesn't kill the new one over a stale code signature.
func (u *updater) install(rel githubRelease, want string) (string, error) {
	name := u.assetName()
	var bin, sum *githubAsset
	for i := range rel.Assets {
		switch rel.Assets[i].Name {
		case name:
			bin = &rel.Assets[i]
		case name + ".sha256":
			sum = &rel.Assets[i]
		}
	}
	if bin == nil {
		return "", fmt.Errorf("release %s has no %s (see %s)", rel.TagName, name, rel.HTMLURL)
	}
	expected, err := u.expectedDigest(bin, sum)
	if err != nil {
		return "", err
	}

	exe, err := u.executable()
	if err != nil {
		return "", fmt.Errorf("finding this hc: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	info, err := os.Stat(exe)
	if err != nil {
		return "", fmt.Errorf("finding this hc: %w", err)
	}
	dir := filepath.Dir(exe)

	ext := ""
	if u.goos == "windows" {
		ext = ".exe"
	}
	tmp, err := os.CreateTemp(dir, ".hc-update-*"+ext)
	if err != nil {
		return "", fmt.Errorf("cannot write next to %s (%v); install the release by hand: %s", exe, err, bin.DownloadURL)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once it has been moved into place

	resp, err := u.get(context.Background(), bin.DownloadURL)
	if err != nil {
		tmp.Close()
		return "", fmt.Errorf("downloading %s: %w", name, err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxBinarySize+1))
	resp.Body.Close()
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", name, err)
	}
	if n > maxBinarySize {
		return "", fmt.Errorf("downloading %s: larger than %d MiB", name, maxBinarySize>>20)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != expected {
		return "", fmt.Errorf("%s does not match its SHA-256 (got %s, want %s); nothing was replaced", name, got, expected)
	}
	if err := os.Chmod(tmpPath, info.Mode().Perm()|0o111); err != nil {
		return "", err
	}
	if u.goos == "darwin" {
		if err := sameSigner(exe, tmpPath); err != nil {
			return "", err
		}
	}
	if err := checkRuns(tmpPath, want); err != nil {
		return "", err
	}
	if err := replaceExecutable(tmpPath, exe, u.goos); err != nil {
		return "", fmt.Errorf("replacing %s: %w", exe, err)
	}
	return exe, nil
}

// expectedDigest is the SHA-256 GitHub reports for the asset, or else the
// one in its .sha256 file. A binary that can't be checked is not installed.
func (u *updater) expectedDigest(bin, sum *githubAsset) (string, error) {
	if hexsum, ok := strings.CutPrefix(bin.Digest, "sha256:"); ok && isSHA256(hexsum) {
		return strings.ToLower(hexsum), nil
	}
	if sum == nil {
		return "", fmt.Errorf("release has no SHA-256 for %s; not installing an unverified binary", bin.Name)
	}
	resp, err := u.get(context.Background(), sum.DownloadURL)
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", sum.Name, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", sum.Name, err)
	}
	fields := strings.Fields(string(body))
	if len(fields) == 0 || !isSHA256(fields[0]) {
		return "", fmt.Errorf("%s does not hold a SHA-256", sum.Name)
	}
	return strings.ToLower(fields[0]), nil
}

func isSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// sameSigner refuses a download signed with another certificate than the
// installed hc: macOS keeps what it allowed hc by that signature, and only
// the project's certificate should replace it. An ad-hoc signed hc (its
// requirement is its own hash) accepts any release.
func sameSigner(installed, downloaded string) error {
	out, err := exec.Command("/usr/bin/codesign", "-d", "-r-", installed).CombinedOutput()
	if err != nil {
		return nil // unsigned: nothing to keep
	}
	req := designatedRequirement(string(out))
	if req == "" {
		return nil
	}
	if out, err := exec.Command("/usr/bin/codesign", "--verify", "--strict", "--test-requirement=="+req, downloaded).CombinedOutput(); err != nil {
		return fmt.Errorf("the downloaded hc is not signed with the certificate of %s (%s); nothing was replaced",
			installed, strings.TrimSpace(string(out)))
	}
	return nil
}

// designatedRequirement is the requirement in "codesign -d -r-" output, or
// "" for an ad-hoc signature, whose requirement is the build's own hash.
func designatedRequirement(codesignOutput string) string {
	for _, line := range strings.Split(codesignOutput, "\n") {
		if req, ok := strings.CutPrefix(strings.TrimSpace(line), "designated => "); ok {
			if strings.HasPrefix(req, "cdhash ") {
				return ""
			}
			return req
		}
	}
	return ""
}

// checkRuns runs the downloaded hc once, so a binary for the wrong platform
// or one the OS refuses to start never replaces a working hc.
func checkRuns(path, want string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return fmt.Errorf("the downloaded hc does not run (%v); nothing was replaced", err)
	}
	if got := strings.TrimSpace(string(out)); got != "hc "+want {
		return fmt.Errorf("the downloaded hc reports %q instead of \"hc %s\"; nothing was replaced", got, want)
	}
	return nil
}

// replaceExecutable moves src over dst. Windows can't overwrite a running
// .exe but can rename it, so the old one steps aside first and is removed by
// the next update.
func replaceExecutable(src, dst, goos string) error {
	if goos != "windows" {
		return os.Rename(src, dst)
	}
	old := dst + ".old"
	os.Remove(old)
	if err := os.Rename(dst, old); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		os.Rename(old, dst)
		return err
	}
	os.Remove(old) // fails while this process still runs it; fine
	return nil
}

// compareVersions compares MAJOR[.MINOR][.PATCH] numerically, a missing part
// counting as 0. ok is false when either side isn't such a version (a "dev"
// build from source).
func compareVersions(a, b string) (cmp int, ok bool) {
	left, okA := versionParts(a)
	right, okB := versionParts(b)
	if !okA || !okB {
		return 0, false
	}
	for i := 0; i < max(len(left), len(right)); i++ {
		var l, r int
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		if l != r {
			if l < r {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

func versionParts(v string) ([]int, bool) {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) < 1 || len(parts) > 3 {
		return nil, false
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || strings.TrimLeft(p, "0123456789") != "" {
			return nil, false
		}
		nums[i] = n
	}
	return nums, true
}
