package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

const trustScreen = "claude --resume s1\n\n Quick safety check: Is this a project you created or one you trust?\n\n ❯ No, exit\n   Yes, I trust this folder\n\n Enter to confirm · Esc to cancel\n\n"

// stopAtTrust makes s1's agent wait at its folder-trust dialog, as one Herdr
// resumed after a restart does.
func stopAtTrust(fh *fakeHerdr) {
	fh.snap.Panes[0].AgentStatus = herdr.StatusBlocked
	fh.visible = trustScreen
}

func lastInteraction(t *testing.T, body []byte) *model.Interaction {
	t.Helper()
	var out struct {
		Messages []model.Message `json:"messages"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Messages) == 0 {
		return nil
	}
	last := out.Messages[len(out.Messages)-1]
	if len(last.Blocks) == 0 || last.Blocks[0].Interaction == nil {
		return nil
	}
	return last.Blocks[0].Interaction
}

func Test信頼確認で止まったセッションはメッセージに信頼の確認が加わり承認するとダイアログを通す(t *testing.T) {
	ts, fp, fh, tok := newTestServer(t)
	stopAtTrust(fh)
	resp, body := do(t, ts, tok, "GET", "/v1/sessions/fake:s1/messages", nil, "")
	ia := lastInteraction(t, body)
	if resp.StatusCode != 200 || ia == nil {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if ia.ID != "trust:w1:p1" || ia.Kind != model.KindTrust || ia.State != model.InteractionPending || !ia.Supported || ia.Detail != fp.root {
		t.Errorf("trust interaction = %+v", ia)
	}
	// The anchor is the trust message itself while the dialog stays.
	if _, body = do(t, ts, tok, "GET", "/v1/sessions/fake:s1/messages?after=trust:w1:p1", nil, ""); lastInteraction(t, body) == nil {
		t.Errorf("after the trust anchor: %s", body)
	}

	resp, body = do(t, ts, tok, "POST", "/v1/sessions/fake:s1/respond", []byte(`{"interactionId":"trust:w1:p1","decision":"approve"}`), "application/json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("respond status %d: %s", resp.StatusCode, body)
	}
	if strings.Join(fh.calls, ",") != "keys:down,enter" || len(fp.resp) != 0 {
		t.Errorf("herdr calls = %v, provider responses = %v", fh.calls, fp.resp)
	}
}

func Test信頼確認を拒否するとセッションのペインを止める(t *testing.T) {
	ts, _, fh, tok := newTestServer(t)
	stopAtTrust(fh)
	resp, body := do(t, ts, tok, "POST", "/v1/sessions/fake:s1/respond", []byte(`{"interactionId":"trust:w1:p1","decision":"deny"}`), "application/json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("respond status %d: %s", resp.StatusCode, body)
	}
	if strings.Join(fh.calls, ",") != "close-workspace:w1" {
		t.Errorf("herdr calls = %v", fh.calls)
	}
}

func Test信頼確認が消えた後や別のペイン宛ての回答はキーを送らない(t *testing.T) {
	ts, _, fh, tok := newTestServer(t)
	stopAtTrust(fh)
	resp, _ := do(t, ts, tok, "POST", "/v1/sessions/fake:s1/respond", []byte(`{"interactionId":"trust:w9:p1","decision":"approve"}`), "application/json")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("another pane: status %d", resp.StatusCode)
	}
	fh.visible = "❯ "
	resp, _ = do(t, ts, tok, "POST", "/v1/sessions/fake:s1/respond", []byte(`{"interactionId":"trust:w1:p1","decision":"approve"}`), "application/json")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("dialog gone: status %d", resp.StatusCode)
	}
	if len(fh.calls) != 0 {
		t.Errorf("herdr calls = %v", fh.calls)
	}
}

func Test信頼確認の文言が会話にあるだけや他の回答待ちがあるときは信頼の確認を出さない(t *testing.T) {
	ts, fp, fh, tok := newTestServer(t)
	stopAtTrust(fh)
	fh.visible = trustScreen + strings.Repeat("later output\n", 20)
	if _, body := do(t, ts, tok, "GET", "/v1/sessions/fake:s1/messages", nil, ""); lastInteraction(t, body) != nil {
		t.Errorf("dialog wording above the bottom: %s", body)
	}

	fh.visible = trustScreen
	fp.pending = &model.Interaction{ID: "q1", Type: model.InteractionApproval, State: model.InteractionPending, Supported: true}
	_, body := do(t, ts, tok, "GET", "/v1/sessions/fake:s1/messages", nil, "")
	if ia := lastInteraction(t, body); ia == nil || ia.ID != "q1" {
		t.Errorf("with a pending approval: %s", body)
	}

	fp.pending = nil
	fh.snap.Panes[0].AgentStatus = herdr.StatusIdle
	if _, body := do(t, ts, tok, "GET", "/v1/sessions/fake:s1/messages", nil, ""); lastInteraction(t, body) != nil {
		t.Errorf("idle session: %s", body)
	}
}
