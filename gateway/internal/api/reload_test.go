package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

type reloadResponse struct {
	Session  model.Session
	Messages []model.Message
	Findings []reloadFinding
}

func reload(t *testing.T, opts ...func(*fakeProvider)) (reloadResponse, *fakeProvider, *deadletter.Recorder) {
	t.Helper()
	return reloadWith(t, `{"shown":[]}`, opts...)
}

func reloadWith(t *testing.T, body string, opts ...func(*fakeProvider)) (reloadResponse, *fakeProvider, *deadletter.Recorder) {
	t.Helper()
	rec := &deadletter.Recorder{}
	ts, fp, _, tok := newTestServer(t, func(s *Server) { s.Sink = rec })
	for _, opt := range opts {
		opt(fp)
	}
	resp, raw := do(t, ts, tok, "POST", "/v1/sessions/fake:s1/reload", []byte(body), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d body %s", resp.StatusCode, raw)
	}
	var got reloadResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	return got, fp, rec
}

func kinds(fs []reloadFinding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Kind)
	}
	return out
}

func question() *model.Interaction {
	return &model.Interaction{ID: "ask-1", Type: model.InteractionQuestions, State: model.InteractionPending, Supported: true}
}

func Test再読込はプロバイダーのキャッシュを捨てて全メッセージを返す(t *testing.T) {
	got, fp, rec := reload(t)
	if len(got.Messages) != 3 || got.Messages[0].ID != "m1" {
		t.Errorf("messages = %+v", got.Messages)
	}
	if !slices.Equal(fp.forgotten, []string{"s1"}) {
		t.Errorf("forgotten = %v", fp.forgotten)
	}
	if len(got.Findings) != 0 || len(rec.Entries) != 0 {
		t.Errorf("findings = %+v, entries = %+v", got.Findings, rec.Entries)
	}
}

func Test再読込でアプリが表示していなかった質問を記録する(t *testing.T) {
	got, _, rec := reload(t, func(fp *fakeProvider) {
		fp.pending = question()
		fp.daemonStatus = model.StatusWaitingInput
	})
	if !slices.Equal(kinds(got.Findings), []string{findingNotShown}) {
		t.Fatalf("findings = %+v", got.Findings)
	}
	if len(rec.Entries) != 1 || rec.Entries[0].Kind != deadletter.MissedInteraction || rec.Entries[0].SessionID != "fake:s1" {
		t.Errorf("entries = %+v", rec.Entries)
	}

	// 表示済みなら何も記録しない。shown を送らない古いクライアントも同じ。
	for _, body := range []string{`{"shown":["ask-1"]}`, ``} {
		got, _, rec = reloadWith(t, body, func(fp *fakeProvider) {
			fp.pending = question()
			fp.daemonStatus = model.StatusWaitingInput
		})
		if len(got.Findings) != 0 || len(rec.Entries) != 0 {
			t.Errorf("body %q: findings = %+v", body, got.Findings)
		}
	}
}

func Test再読込でキャッシュした読み取りに無かった質問を記録する(t *testing.T) {
	got, _, rec := reloadWith(t, `{"shown":["ask-1"]}`, func(fp *fakeProvider) {
		fp.pending = question()
		fp.staleCache = true
		fp.daemonStatus = model.StatusWaitingInput
	})
	if !slices.Equal(kinds(got.Findings), []string{findingStaleCache}) || len(rec.Entries) != 1 {
		t.Errorf("findings = %+v, entries = %+v", got.Findings, rec.Entries)
	}
	if n := len(got.Messages); n != 4 || got.Messages[n-1].ID != "q1" {
		t.Errorf("再読込の結果に質問がない: %+v", got.Messages)
	}
}

func Test回答待ちなのにアプリで答えられる質問がなければ画面と一緒に記録する(t *testing.T) {
	for name, pending := range map[string]*model.Interaction{
		"質問なし":    nil,
		"ターミナルのみ": {ID: "blocked", Type: model.InteractionApproval, State: model.InteractionPending},
	} {
		t.Run(name, func(t *testing.T) {
			got, _, rec := reloadWith(t, `{"shown":["blocked"]}`, func(fp *fakeProvider) {
				fp.pending = pending
				fp.daemonStatus = model.StatusWaitingApproval
			})
			if !slices.Equal(kinds(got.Findings), []string{findingUncaptured}) || len(rec.Entries) != 1 {
				t.Fatalf("findings = %+v, entries = %+v", got.Findings, rec.Entries)
			}
			var raw struct {
				PaneID string `json:"paneId"`
				Screen string `json:"screen"`
			}
			json.Unmarshal(rec.Entries[0].Raw, &raw)
			if raw.PaneID != "w1:p1" || raw.Screen != "$ hello" {
				t.Errorf("raw = %s", rec.Entries[0].Raw)
			}
		})
	}
}

func Test再読込は不正なJSONと存在しないセッションを拒否する(t *testing.T) {
	ts, _, _, tok := newTestServer(t)
	resp, _ := do(t, ts, tok, "POST", "/v1/sessions/fake:s1/reload", []byte(`{`), "application/json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid json status = %d", resp.StatusCode)
	}
	resp, _ = do(t, ts, tok, "POST", "/v1/sessions/fake:missing/reload", nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing status = %d", resp.StatusCode)
	}
}
