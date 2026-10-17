package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/sessions"
)

type queuedMessageProvider struct {
	*fakeProvider
	messages []model.Message
}

func (p *queuedMessageProvider) Messages(context.Context, string, *providers.Live) ([]model.Message, error) {
	return p.messages, nil
}

func Testキューの手前に追加した質問も差分取得で返しETagを更新する(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	queued := model.Message{ID: "queued:2026-10-17T00:00:00Z", Queued: true}
	p := &queuedMessageProvider{fakeProvider: &fakeProvider{}, messages: []model.Message{{ID: "m1"}, queued}}
	srv := &Server{Config: cfg, Sessions: sessions.New(&fakeHerdr{snap: &herdr.Snapshot{}}, 0, p)}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	path := "/v1/sessions/fake:s1/messages?after=" + queued.ID
	get := func(tag string) (*http.Response, []model.Message) {
		t.Helper()
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+cfg.Get().AuthToken)
		if tag != "" {
			req.Header.Set("If-None-Match", tag)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		var page struct{ Messages []model.Message }
		if len(body) > 0 {
			if err := json.Unmarshal(body, &page); err != nil {
				t.Fatal(err)
			}
		}
		return resp, page.Messages
	}
	first, msgs := get("")
	if first.StatusCode != 200 || len(msgs) != 2 || msgs[0].ID != "m1" {
		t.Fatalf("initial page: status %d, %+v", first.StatusCode, msgs)
	}
	question := model.Message{ID: "question", Blocks: []model.Block{{Type: model.BlockInteraction, Interaction: &model.Interaction{ID: "ask", Type: model.InteractionQuestions, State: model.InteractionPending}}}}
	p.messages = []model.Message{{ID: "m1"}, question, queued}
	second, msgs := get(first.Header.Get("ETag"))
	if second.StatusCode != 200 || len(msgs) != 3 || msgs[1].ID != "question" {
		t.Fatalf("question missed: status %d, %+v", second.StatusCode, msgs)
	}
	if second.Header.Get("ETag") == first.Header.Get("ETag") {
		t.Fatal("question did not change ETag")
	}
	unchanged, _ := get(second.Header.Get("ETag"))
	if unchanged.StatusCode != http.StatusNotModified {
		t.Fatalf("unchanged status = %d", unchanged.StatusCode)
	}
}
