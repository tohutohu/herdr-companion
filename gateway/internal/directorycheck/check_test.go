package directorycheck

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

type fakeHistory struct {
	summaries []providers.Summary
	messages  map[string][]model.Message
	read      []string
}

func (f *fakeHistory) Recent(context.Context, time.Time) ([]providers.Summary, error) {
	return f.summaries, nil
}
func (f *fakeHistory) Messages(_ context.Context, id string, _ *providers.Live) ([]model.Message, error) {
	f.read = append(f.read, id)
	return f.messages[id], nil
}
func message(role model.Role, text string) model.Message {
	return model.Message{Role: role, Blocks: []model.Block{model.TextBlock(text)}}
}
func root(t *testing.T) string { t.Helper(); dir, _ := filepath.EvalSymlinks(t.TempDir()); return dir }

func Test同じ実ディレクトリの最新履歴と安全な資料だけ送る(t *testing.T) {
	dir := root(t)
	outside := root(t)
	os.WriteFile(filepath.Join(outside, "secret"), []byte("PRIVATE"), 0600)
	os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "AGENTS.md"))
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("Android通知アプリ"), 0600)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET"), 0600)
	alias := filepath.Join(root(t), "alias")
	os.Symlink(dir, alias)
	h := &fakeHistory{messages: map[string][]model.Message{}}
	for i, id := range []string{"old", "one", "two", "three", "other"} {
		cwd := alias
		if id == "other" {
			cwd = outside
		}
		h.summaries = append(h.summaries, providers.Summary{NativeID: id, Cwd: cwd, UpdatedAt: time.Now().Add(time.Duration(i) * time.Minute)})
		h.messages[id] = []model.Message{message(model.RoleUser, "通知を直して "+id), message(model.RoleTool, "PRIVATE TOOL OUTPUT"), message(model.RoleAssistant, "通知を修正した "+id), message(model.RoleAssistant, "$ cat PRIVATE")}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing auth")
		}
		var req struct {
			State evidence `json:"state"`
			Model string   `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		b, _ := json.Marshal(req.State)
		if strings.Contains(string(b), "PRIVATE") || strings.Contains(string(b), "SECRET") || strings.Contains(string(b), "other") {
			t.Errorf("unrelated/private context: %s", b)
		}
		if req.Model != "jev-latest" || req.State.Files["README.md"] != "Android通知アプリ" || len(req.State.History) != 3 {
			t.Errorf("bad state: %+v", req)
		}
		if req.State.History[0].Instructions[0] != "通知を直して three" || req.State.History[0].LastReport != "通知を修正した three" {
			t.Errorf("wrong history: %+v", req.State.History)
		}
		w.Write([]byte(`{"answers":{"fit":{"type":"choice","choice":"mismatch","probabilities":{"match":0.01,"mismatch":0.98,"unknown":0.01},"confidence":0.9}}}`))
	}))
	defer srv.Close()
	c := Checker{APIKey: "test-key", Endpoint: srv.URL, Providers: []History{h}}
	got := c.Check(context.Background(), dir, "ブログの公開日を変えて")
	if got.Verdict != "mismatch" || got.HistoryCount != 3 {
		t.Fatalf("%+v", got)
	}
	if strings.Join(h.read, ",") != "three,two,one" {
		t.Fatal(h.read)
	}
}

func Test明確な不一致だけ警告し障害や不正な応答は判定不能(t *testing.T) {
	dir := root(t)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("app"), 0600)
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"一致", `{"answers":{"fit":{"type":"choice","choice":"match","probabilities":{"match":0.96,"mismatch":0.01,"unknown":0.03},"confidence":0.9}}}`, "match", 200},
		{"弱い不一致", `{"answers":{"fit":{"type":"choice","choice":"mismatch","probabilities":{"match":0.1,"mismatch":0.8,"unknown":0.1},"confidence":0.8}}}`, "unknown", 200},
		{"確信不足", `{"answers":{"fit":{"type":"choice","choice":"mismatch","probabilities":{"match":0.01,"mismatch":0.98,"unknown":0.01},"confidence":0.5}}}`, "unknown", 200},
		{"項目欠落", `{"answers":{}}`, "unavailable", 200},
		{"確率範囲外", `{"answers":{"fit":{"type":"choice","choice":"mismatch","probabilities":{"match":0,"mismatch":2,"unknown":0},"confidence":1}}}`, "unavailable", 200},
		{"壊れたJSON", `{`, "unavailable", 200},
		{"レート制限", `rate limited`, "unavailable", 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write([]byte(tc.body)) }))
			defer srv.Close()
			c := Checker{APIKey: "key", Endpoint: srv.URL}
			if got := c.Check(context.Background(), dir, "依頼"); got.Verdict != tc.want {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func Test未設定や空の指示や空フォルダでは外部APIを呼ばない(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected API call") }))
	defer srv.Close()
	dir := root(t)
	c := Checker{Endpoint: srv.URL}
	if c.Check(context.Background(), dir, "依頼").Verdict != "disabled" {
		t.Fatal("disabled")
	}
	c.APIKey = "key"
	for _, prompt := range []string{"", "依頼", strings.Repeat("あ", 6001)} {
		if c.Check(context.Background(), dir, prompt).Verdict != "unknown" {
			t.Fatal("unknown")
		}
	}
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("app"), 0600)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c.Check(ctx, dir, "依頼").Verdict != "unavailable" {
		t.Fatal("cancelled")
	}
}
