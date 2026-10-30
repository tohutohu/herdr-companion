package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// The test binary doubles as a fake herdr-claude-sidecar: run with
// HERDR_FAKE_SIDECAR=1 it speaks the sidecar protocol from a script and
// logs every request to HERDR_FAKE_SIDECAR_LOG.
func TestMain(m *testing.M) {
	if os.Getenv("HERDR_FAKE_SIDECAR") == "1" {
		fakeSidecar()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeSidecar() {
	logf, _ := os.OpenFile(os.Getenv("HERDR_FAKE_SIDECAR_LOG"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	out := json.NewEncoder(os.Stdout)
	emit := func(sid, ev string, data map[string]any) {
		out.Encode(map[string]any{"event": ev, "sessionId": sid, "data": data})
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	residents := map[string]bool{}
	for sc.Scan() {
		logf.Write(append(sc.Bytes(), '\n'))
		var req struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		json.Unmarshal(sc.Bytes(), &req)
		sid, _ := req.Params["sessionId"].(string)
		reply := func(v any) { out.Encode(map[string]any{"id": req.ID, "result": v}) }
		switch req.Method {
		case "start":
			if req.Params["resident"] == true {
				residents[sid] = true
			}
			sub := "Claude Max"
			if os.Getenv("HERDR_FAKE_SIDECAR_NOLOGIN") == "1" {
				reply(map[string]any{"permissionMode": "default", "account": map[string]any{}})
				continue
			}
			reply(map[string]any{"permissionMode": "default", "claudeCodeVersion": "2.1.312", "account": map[string]any{"subscriptionType": sub},
				"models": []map[string]any{
					{"value": "sonnet", "resolvedModel": "claude-sonnet-5-5", "supportsAutoMode": true},
					{"value": "claude-haiku-4-5-20251001", "resolvedModel": "claude-haiku-4-5-20251001", "supportsAutoMode": false},
				}})
		case "send":
			reply(map[string]any{})
			text, _ := req.Params["text"].(string)
			if residents[sid] {
				fakeResidentTurn(text, func(ev string, data map[string]any) { emit(sid, ev, data) })
				continue
			}
			emit(sid, "state", map[string]any{"state": "running"})
			switch {
			case strings.Contains(text, "crash"):
				os.Exit(1)
			case strings.Contains(text, "ask"):
				emit(sid, "permission", map[string]any{"requestId": "toolu_ask", "toolName": "AskUserQuestion",
					"input": map[string]any{"questions": []map[string]any{{"question": "Which?", "header": "Color", "multiSelect": false,
						"options": []map[string]any{{"label": "Red", "description": ""}, {"label": "Blue", "description": ""}}}}}})
			case strings.Contains(text, "bash"):
				emit(sid, "permission", map[string]any{"requestId": "toolu_bash", "toolName": "Bash", "title": "Claude wants to run make",
					"input": map[string]any{"command": "make"}, "canAlwaysAllow": true})
			case strings.Contains(text, "plan"):
				if m, ok := strings.CutPrefix(text, "plan with "); ok {
					emit(sid, "messageStart", map[string]any{"messageId": "msg_plan", "model": m})
				}
				emit(sid, "permission", map[string]any{"requestId": "toolu_plan", "toolName": "ExitPlanMode", "input": map[string]any{"plan": "1. do it"}})
			case strings.Contains(text, "stream"):
				emit(sid, "messageStart", map[string]any{"messageId": "msg_1", "model": "claude-haiku-5-5"})
				emit(sid, "delta", map[string]any{"text": "Hel"})
				emit(sid, "delta", map[string]any{"text": "lo"})
				emit(sid, "state", map[string]any{"state": "running"})
			default:
				emit(sid, "result", map[string]any{"subtype": "success", "isError": false})
				emit(sid, "state", map[string]any{"state": "idle"})
			}
		case "respond":
			reply(map[string]any{})
			emit(sid, "permissionResolved", map[string]any{"requestId": req.Params["requestId"]})
		case "stop":
			reply(map[string]any{})
			emit(sid, "exit", map[string]any{"error": nil})
		default:
			reply(map[string]any{})
		}
	}
}

// fakeResidentTurn answers a resident run's prompt.
func fakeResidentTurn(text string, emit func(string, map[string]any)) {
	emit("init", map[string]any{"model": "claude-sonnet-5-5"})
	if strings.Contains(text, "hang") {
		return
	}
	var denials []string
	if strings.Contains(text, "deny") {
		emit("denied", map[string]any{"requestId": "toolu_1", "toolName": "Bash", "input": "gh run list"})
		denials = append(denials, "Bash")
	}
	emit("result", map[string]any{"subtype": "success", "isError": false, "numTurns": 2, "result": "done",
		"structuredOutput": map[string]any{"schemaVersion": 1, "outcome": "unchanged", "summary": "変化なし"},
		"usage":            map[string]any{"input_tokens": 100, "output_tokens": 20, "cache_read_input_tokens": 50},
		"totalCostUsd":     0.02, "durationMs": 1500, "permissionDenials": denials})
}

func newSidecarProvider(t *testing.T) (*Provider, string) {
	t.Helper()
	log := filepath.Join(t.TempDir(), "sidecar.log")
	t.Setenv("HERDR_FAKE_SIDECAR", "1")
	t.Setenv("HERDR_FAKE_SIDECAR_LOG", log)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := New(t.TempDir(), deadletter.Nop{})
	p.SetDirect(DirectConfig{Sidecar: exe, Claude: "/usr/bin/true"})
	t.Cleanup(p.Close)
	return p, log
}

func sidecarRequests(t *testing.T, log, method string) []map[string]any {
	t.Helper()
	b, _ := os.ReadFile(log)
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var req struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal([]byte(line), &req) == nil && req.Method == method {
			out = append(out, req.Params)
		}
	}
	return out
}

func info(p *Provider, id string) providers.DirectInfo {
	for _, in := range p.DirectSessions() {
		if in.NativeID == id {
			return in
		}
	}
	return providers.DirectInfo{}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

var direct = &providers.Live{State: "working"}

func Testサイドカーで開始したセッションはIDを先に決め応答をストリーミング表示する(t *testing.T) {
	p, log := newSidecarProvider(t)
	ctx := context.Background()
	id, err := p.DirectStart(ctx, providers.DirectStart{Cwd: "/w", Model: "haiku", Mode: "manual", Prompt: model.Input{Text: "stream please"}})
	if err != nil || !sessionIDPattern.MatchString(id) {
		t.Fatalf("start = %q, %v", id, err)
	}
	start := sidecarRequests(t, log, "start")[0]
	if start["sessionId"] != id || start["resume"] != false || start["model"] != "haiku" || start["permissionMode"] != "default" {
		t.Errorf("start params = %v", start)
	}
	env, _ := start["env"].(map[string]any)
	if env["HC_SESSION_ID"] != "claude:"+id || env["CLAUDECODE"] != nil {
		t.Errorf("env HC_SESSION_ID=%v CLAUDECODE=%v", env["HC_SESSION_ID"], env["CLAUDECODE"])
	}
	eventually(t, "streamed text", func() bool {
		msgs, _ := p.Messages(ctx, id, direct)
		return len(msgs) == 1 && msgs[0].ID == streamingPrefix+"msg_1" && msgs[0].Blocks[0].Text == "Hello"
	})
	if in := info(p, id); !in.Alive || !in.Running || in.Model != "claude-haiku-5-5" {
		t.Errorf("info = %+v", in)
	}
}

func Test質問への回答はAskUserQuestionのanswersとして返す(t *testing.T) {
	p, log := newSidecarProvider(t)
	ctx := context.Background()
	id, _ := p.DirectStart(ctx, providers.DirectStart{Cwd: "/w", Prompt: model.Input{Text: "ask me"}})
	eventually(t, "question", func() bool { return info(p, id).Waiting })
	sum, err := p.Summary(ctx, id, direct)
	if err == nil && sum.Pending != model.InteractionQuestions {
		t.Errorf("pending = %q", sum.Pending)
	}
	msgs, _ := p.Messages(ctx, id, direct)
	ia := findInteraction(msgs, "toolu_ask")
	if ia == nil || ia.State != model.InteractionPending || ia.Questions[0].Question != "Which?" {
		t.Fatalf("interaction = %+v", ia)
	}
	if err := p.Respond(ctx, id, direct, model.InteractionResponse{InteractionID: "toolu_ask", Answers: map[string]model.Answer{"0": {Selected: []string{"Blue"}}}}); err != nil {
		t.Fatal(err)
	}
	r := sidecarRequests(t, log, "respond")[0]
	in, _ := r["updatedInput"].(map[string]any)
	answers, _ := in["answers"].(map[string]any)
	if r["behavior"] != "allow" || answers["Which?"] != "Blue" || in["questions"] == nil {
		t.Errorf("respond = %v", r)
	}
	if info(p, id).Waiting {
		t.Error("still waiting")
	}
	if err := p.Respond(ctx, id, direct, model.InteractionResponse{InteractionID: "toolu_ask", Answers: map[string]model.Answer{"0": {Selected: []string{"Red"}}}}); err != providers.ErrInteractionGone {
		t.Errorf("second answer err = %v", err)
	}
}

func Test承認はこのセッションに限って許可できる(t *testing.T) {
	p, log := newSidecarProvider(t)
	ctx := context.Background()
	id, _ := p.DirectStart(ctx, providers.DirectStart{Cwd: "/w", Prompt: model.Input{Text: "bash it"}})
	eventually(t, "approval", func() bool { return info(p, id).Waiting })
	msgs, _ := p.Messages(ctx, id, direct)
	ia := findInteraction(msgs, "toolu_bash")
	if ia == nil || ia.Type != model.InteractionApproval || ia.Title != "Claude wants to run make" || len(ia.Decisions) != 3 {
		t.Fatalf("interaction = %+v", ia)
	}
	if err := p.Respond(ctx, id, direct, model.InteractionResponse{InteractionID: "toolu_bash", Decision: model.DecisionApproveSession}); err != nil {
		t.Fatal(err)
	}
	if r := sidecarRequests(t, log, "respond")[0]; r["behavior"] != "allow" || r["alwaysAllow"] != true {
		t.Errorf("respond = %v", r)
	}
}

func Testプランの承認はモードを切り替えて許可する(t *testing.T) {
	p, log := newSidecarProvider(t)
	ctx := context.Background()
	id, _ := p.DirectStart(ctx, providers.DirectStart{Cwd: "/w", Mode: "plan", Prompt: model.Input{Text: "plan it"}})
	eventually(t, "plan", func() bool { return info(p, id).Waiting })
	msgs, _ := p.Messages(ctx, id, direct)
	if ia := findInteraction(msgs, "toolu_plan"); ia == nil || ia.Kind != model.KindPlan {
		t.Fatalf("plan card missing: %+v", msgs)
	}
	if err := p.Respond(ctx, id, direct, model.InteractionResponse{InteractionID: "toolu_plan", Answers: map[string]model.Answer{"0": {Selected: []string{planAutoAccept}}}}); err != nil {
		t.Fatal(err)
	}
	if r := sidecarRequests(t, log, "respond")[0]; r["behavior"] != "allow" {
		t.Errorf("respond = %v", r)
	}
	if m := sidecarRequests(t, log, "setMode"); len(m) != 1 || m[0]["mode"] != "acceptEdits" {
		t.Errorf("setMode = %v", m)
	}
	// フィードバック付きの却下はプランモードのまま理由を伝える
	p.directSend(ctx, id, model.Input{Text: "plan again"})
	eventually(t, "plan", func() bool { return info(p, id).Waiting })
	p.Respond(ctx, id, direct, model.InteractionResponse{InteractionID: "toolu_plan", Answers: map[string]model.Answer{"0": {Text: "smaller steps"}}})
	if r := sidecarRequests(t, log, "respond")[1]; r["behavior"] != "deny" || !strings.Contains(r["message"].(string), "smaller steps") {
		t.Errorf("respond = %v", r)
	}
}

func Testオートモードが使えるモデルではプランをオートモードで承認できる(t *testing.T) {
	p, log := newSidecarProvider(t)
	ctx := context.Background()
	id, _ := p.DirectStart(ctx, providers.DirectStart{Cwd: "/w", Mode: "plan", Prompt: model.Input{Text: "plan with claude-sonnet-5-5[1m]"}})
	eventually(t, "plan", func() bool { return info(p, id).Waiting })
	msgs, _ := p.Messages(ctx, id, direct)
	ia := findInteraction(msgs, "toolu_plan")
	if ia == nil || ia.Questions[0].Options[0].Label != planAutoMode || ia.Questions[0].Options[1].Label != planManual {
		t.Fatalf("plan card = %+v", ia)
	}
	if err := p.Respond(ctx, id, direct, model.InteractionResponse{InteractionID: "toolu_plan", Answers: map[string]model.Answer{"0": {Selected: []string{planAutoMode}}}}); err != nil {
		t.Fatal(err)
	}
	if r := sidecarRequests(t, log, "respond")[0]; r["behavior"] != "allow" {
		t.Errorf("respond = %v", r)
	}
	if m := sidecarRequests(t, log, "setMode"); len(m) != 1 || m[0]["mode"] != "auto" {
		t.Errorf("setMode = %v", m)
	}
}

func Testオートモード非対応のモデルではプランの選択肢に編集の自動承認を出す(t *testing.T) {
	p, _ := newSidecarProvider(t)
	ctx := context.Background()
	id, _ := p.DirectStart(ctx, providers.DirectStart{Cwd: "/w", Mode: "plan", Prompt: model.Input{Text: "plan with claude-haiku-4-5-20251001"}})
	eventually(t, "plan", func() bool { return info(p, id).Waiting })
	msgs, _ := p.Messages(ctx, id, direct)
	if ia := findInteraction(msgs, "toolu_plan"); ia == nil || ia.Questions[0].Options[0].Label != planAutoAccept {
		t.Fatalf("plan card = %+v", ia)
	}
}

func Testサイドカーが落ちると実行中のターンは失敗として扱い再送しない(t *testing.T) {
	p, log := newSidecarProvider(t)
	ctx := context.Background()
	id, _ := p.DirectStart(ctx, providers.DirectStart{Cwd: "/w"})
	if err := p.Send(ctx, id, direct, model.Input{Text: "crash now"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "exit", func() bool { return !info(p, id).Alive })
	in := info(p, id)
	if !in.LastTurnFailed || in.Running || in.Error == "" {
		t.Errorf("info = %+v", in)
	}
	if n := len(sidecarRequests(t, log, "send")); n != 1 {
		t.Errorf("sends = %d", n)
	}
}

func Testログインしていなければ開始を拒む(t *testing.T) {
	p, _ := newSidecarProvider(t)
	t.Setenv("HERDR_FAKE_SIDECAR_NOLOGIN", "1")
	_, err := p.DirectStart(context.Background(), providers.DirectStart{Cwd: "/w"})
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("err = %v", err)
	}
	if len(p.DirectSessions()) != 0 {
		t.Error("refused session kept")
	}
}

func TestAPIキーでの従量課金は許可がなければ拒む(t *testing.T) {
	key, sub, first := "ANTHROPIC_API_KEY", "Claude Max", "firstParty"
	r := startReply{}
	r.Account.APIKeySource, r.Account.APIProvider = &key, &first
	if p := billingProblem(r, false); !strings.Contains(p, "API key") {
		t.Errorf("api key: %q", p)
	}
	if p := billingProblem(r, true); p != "" {
		t.Errorf("allowed: %q", p)
	}
	r = startReply{}
	r.Account.SubscriptionType = &sub
	if p := billingProblem(r, false); p != "" {
		t.Errorf("subscription: %q", p)
	}
	if env := withoutAPIKeys([]string{"ANTHROPIC_API_KEY=x", "PATH=/bin"}); len(env) != 1 || env[0] != "PATH=/bin" {
		t.Errorf("env = %v", env)
	}
}

func Test記録済みの質問カードは回答待ちのカードに置き換え二重に表示しない(t *testing.T) {
	closed := &model.Interaction{ID: "toolu_ask", Type: model.InteractionQuestions, State: model.InteractionClosed}
	msgs := []model.Message{{ID: "m1", Role: model.RoleAssistant, Blocks: []model.Block{{Type: model.BlockInteraction, Interaction: closed}}}}
	req := &permissionRequest{ID: "toolu_ask", ToolName: toolAskUserQuestion,
		Input: json.RawMessage(`{"questions":[{"question":"Which?","header":"C","multiSelect":false,"options":[{"label":"A","description":""}]}]}`)}
	out := withDirectState(msgs, []*permissionRequest{req}, "msg_1", "partial", time.Now(), true)
	if len(out) != 1 || out[0].Blocks[0].Interaction.State != model.InteractionPending {
		t.Fatalf("out = %+v", out)
	}
	if msgs[0].Blocks[0].Interaction.State != model.InteractionClosed {
		t.Error("shared messages modified")
	}
	// 記録に未反映のストリーミング中の応答だけを末尾に足す
	out = withDirectState(msgs, nil, "msg_2", "Hi", time.Now(), false)
	if len(out) != 2 || out[1].ID != streamingPrefix+"msg_2" {
		t.Errorf("streaming = %+v", out)
	}
}

func findInteraction(msgs []model.Message, id string) *model.Interaction {
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == model.BlockInteraction && b.Interaction.ID == id {
				return b.Interaction
			}
		}
	}
	return nil
}
