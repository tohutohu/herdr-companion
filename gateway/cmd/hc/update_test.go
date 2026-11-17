package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeRelease serves a GitHub "latest release" and its hc assets.
type fakeRelease struct {
	tag    string
	binary []byte
	// digest overrides the asset's "sha256:…"; "-" leaves it out.
	digest string
	// sumFile is the .sha256 asset's body; "" leaves the asset out.
	sumFile    string
	downloaded bool
}

func (f *fakeRelease) server(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			digest := f.digest
			if digest == "" {
				sum := sha256.Sum256(f.binary)
				digest = "sha256:" + hex.EncodeToString(sum[:])
			} else if digest == "-" {
				digest = ""
			}
			assets := []githubAsset{{Name: "hc-testos-testarch", DownloadURL: srv.URL + "/hc", Digest: digest}}
			if f.sumFile != "" {
				assets = append(assets, githubAsset{Name: "hc-testos-testarch.sha256", DownloadURL: srv.URL + "/hc.sha256"})
			}
			json.NewEncoder(w).Encode(githubRelease{TagName: f.tag, HTMLURL: srv.URL + "/release", Assets: assets})
		case "/hc":
			f.downloaded = true
			w.Write(f.binary)
		case "/hc.sha256":
			w.Write([]byte(f.sumFile))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeHC is a stand-in for a released hc: a script that prints its version.
func fakeHC(v string) []byte {
	return []byte("#!/bin/sh\necho 'hc " + v + "'\n")
}

// updateApp runs hc update against rel with an installed hc at the returned path.
func updateApp(t *testing.T, current string, rel *fakeRelease) (*app, *bytes.Buffer, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake release binary is a shell script")
	}
	old := version
	version = current
	t.Cleanup(func() { version = old })
	exe := filepath.Join(t.TempDir(), "hc")
	if err := os.WriteFile(exe, []byte("old hc"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := rel.server(t)
	var out bytes.Buffer
	a := &app{out: &out, errOut: &out, updater: &updater{
		api:        srv.URL + "/latest",
		http:       srv.Client(),
		executable: func() (string, error) { return exe, nil },
		goos:       "testos",
		goarch:     "testarch",
	}}
	return a, &out, exe
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func Test新しいリリースがあれば実行中のhcを置き換える(t *testing.T) {
	rel := &fakeRelease{tag: "v2.6.0", binary: fakeHC("2.6.0")}
	a, out, exe := updateApp(t, "2.5.0", rel)
	if code := a.run([]string{"update"}); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if got := readFile(t, exe); got != string(rel.binary) {
		t.Fatalf("hc was not replaced: %q", got)
	}
	if !strings.Contains(out.String(), "updated hc 2.5.0 → 2.6.0") {
		t.Fatalf("output: %s", out)
	}
	entries, _ := os.ReadDir(filepath.Dir(exe))
	if len(entries) != 1 {
		t.Fatalf("left files behind: %v", entries)
	}
}

func Testシンボリックリンク経由でもリンク先を置き換える(t *testing.T) {
	rel := &fakeRelease{tag: "v2.6.0", binary: fakeHC("2.6.0")}
	a, out, exe := updateApp(t, "2.5.0", rel)
	link := filepath.Join(t.TempDir(), "hc")
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}
	a.updater.executable = func() (string, error) { return link, nil }
	if code := a.run([]string{"update"}); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symlink was replaced: %v", err)
	}
	if got := readFile(t, exe); got != string(rel.binary) {
		t.Fatalf("link target was not replaced: %q", got)
	}
}

func Test最新版なら何もダウンロードしない(t *testing.T) {
	rel := &fakeRelease{tag: "v2.5.0", binary: fakeHC("2.5.0")}
	a, out, exe := updateApp(t, "2.5.0", rel)
	if code := a.run([]string{"update"}); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if rel.downloaded || readFile(t, exe) != "old hc" {
		t.Fatal("an up-to-date hc was replaced")
	}
	if !strings.Contains(out.String(), "hc 2.5.0 is up to date") {
		t.Fatalf("output: %s", out)
	}
}

func Test確認だけなら置き換えずに新しい版を知らせる(t *testing.T) {
	rel := &fakeRelease{tag: "v2.6.0", binary: fakeHC("2.6.0")}
	a, out, exe := updateApp(t, "2.5.0", rel)
	if code := a.run([]string{"update", "--check", "--json"}); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if rel.downloaded || readFile(t, exe) != "old hc" {
		t.Fatal("--check replaced hc")
	}
	var res updateResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if res != (updateResult{Current: "2.5.0", Latest: "2.6.0", UpdateAvailable: true}) {
		t.Fatalf("result: %+v", res)
	}
}

func Testソースからビルドしたhcはforceなしでは置き換えない(t *testing.T) {
	rel := &fakeRelease{tag: "v2.6.0", binary: fakeHC("2.6.0")}
	a, out, exe := updateApp(t, "dev", rel)
	if code := a.run([]string{"update"}); code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if readFile(t, exe) != "old hc" || !strings.Contains(out.String(), "hc update --force") {
		t.Fatalf("dev hc was replaced or no hint: %s", out)
	}
	out.Reset()
	if code := a.run([]string{"update", "--force"}); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if readFile(t, exe) != string(rel.binary) {
		t.Fatal("--force did not replace the dev hc")
	}
}

func TestSHA256が合わなければ置き換えない(t *testing.T) {
	rel := &fakeRelease{tag: "v2.6.0", binary: fakeHC("2.6.0"), digest: "sha256:" + strings.Repeat("0", 64)}
	a, out, exe := updateApp(t, "2.5.0", rel)
	if code := a.run([]string{"update"}); code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if readFile(t, exe) != "old hc" || !strings.Contains(out.String(), "does not match its SHA-256") {
		t.Fatalf("output: %s", out)
	}
	entries, _ := os.ReadDir(filepath.Dir(exe))
	if len(entries) != 1 {
		t.Fatalf("left the download behind: %v", entries)
	}
}

func Testダイジェストが無ければsha256ファイルで照合する(t *testing.T) {
	bin := fakeHC("2.6.0")
	sum := sha256.Sum256(bin)
	rel := &fakeRelease{tag: "v2.6.0", binary: bin, digest: "-", sumFile: hex.EncodeToString(sum[:]) + "  hc-testos-testarch\n"}
	a, out, exe := updateApp(t, "2.5.0", rel)
	if code := a.run([]string{"update"}); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if readFile(t, exe) != string(bin) {
		t.Fatal("hc was not replaced")
	}
}

func Test照合できないバイナリは入れない(t *testing.T) {
	rel := &fakeRelease{tag: "v2.6.0", binary: fakeHC("2.6.0"), digest: "-"}
	a, out, exe := updateApp(t, "2.5.0", rel)
	if code := a.run([]string{"update"}); code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if rel.downloaded || readFile(t, exe) != "old hc" {
		t.Fatalf("an unverified binary was installed: %s", out)
	}
}

func Test起動して期待の版を名乗らないバイナリは入れない(t *testing.T) {
	rel := &fakeRelease{tag: "v2.6.0", binary: fakeHC("2.5.9")}
	a, out, exe := updateApp(t, "2.5.0", rel)
	if code := a.run([]string{"update"}); code != 1 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if readFile(t, exe) != "old hc" || !strings.Contains(out.String(), "nothing was replaced") {
		t.Fatalf("output: %s", out)
	}
}

func Test版は数値で比べる(t *testing.T) {
	cases := []struct {
		a, b string
		cmp  int
		ok   bool
	}{
		{"2.5.0", "2.10.0", -1, true},
		{"2.5", "2.5.0", 0, true},
		{"v3.0.0", "2.9.9", 1, true},
		{"dev", "2.5.0", 0, false},
		{"2.5.0-rc1", "2.5.0", 0, false},
	}
	for _, c := range cases {
		cmp, ok := compareVersions(c.a, c.b)
		if cmp != c.cmp || ok != c.ok {
			t.Errorf("compareVersions(%q, %q) = %d, %v; want %d, %v", c.a, c.b, cmp, ok, c.cmp, c.ok)
		}
	}
}

func Test署名の要件はアドホック署名なら空になる(t *testing.T) {
	signed := "Executable=/x/hc\ndesignated => identifier \"com.tohutohu.herdrcompanion.hc\" and certificate leaf = H\"1ff2\"\n"
	if got := designatedRequirement(signed); got != `identifier "com.tohutohu.herdrcompanion.hc" and certificate leaf = H"1ff2"` {
		t.Errorf("signed: %q", got)
	}
	if got := designatedRequirement("Executable=/x/hc\n# designated => cdhash H\"675a\"\n"); got != "" {
		t.Errorf("ad hoc: %q", got)
	}
}

func Test別の証明書で署名されたhcには置き換えない(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("codesign is macOS only")
	}
	dir := t.TempDir()
	installed, other := filepath.Join(dir, "installed"), filepath.Join(dir, "other")
	for _, p := range []string{installed, other} {
		if err := os.WriteFile(p, fakeHC("2.6.0"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Ad hoc: any release may replace it.
	if out, err := exec.Command("codesign", "--force", "--sign", "-", installed).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if err := sameSigner(installed, other); err != nil {
		t.Fatalf("ad hoc installed hc refused an update: %v", err)
	}
	// An installed hc whose requirement other can't meet.
	if out, err := exec.Command("codesign", "--force", "--sign", "-", "-r=designated => identifier \"x\" and certificate leaf = H\"0000000000000000000000000000000000000000\"", installed).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if err := sameSigner(installed, other); err == nil || !strings.Contains(err.Error(), "nothing was replaced") {
		t.Fatalf("err = %v", err)
	}
}
