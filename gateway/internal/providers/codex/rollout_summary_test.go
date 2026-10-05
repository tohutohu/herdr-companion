package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

func summaryTestThread(t *testing.T) (*Thread, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(testdata, "summary_rollout.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	th := &Thread{ID: "thread-000001", Path: path, Cwd: "/work/app", Model: "gpt-6.1-sol", Effort: ptrString("high"), Turns: []Turn{{ID: "turn-1", Status: "completed", Items: []json.RawMessage{
		json.RawMessage(`{"type":"userMessage","id":"user-1","content":[{"type":"text","text":"テストしてください"}]}`),
		json.RawMessage(`{"type":"commandExecution","id":"cmd-1","command":"go test ./...","status":"completed","aggregatedOutput":"ok","exitCode":0}`),
		json.RawMessage(`{"type":"agentMessage","id":"answer-1","text":"テストが成功しました"}`),
	}}}}
	return th, raw
}

func ptrString(s string) *string { return &s }

func appendRollout(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func Test一覧の要約は追記分だけ読み終了結果を一度だけ照合する(t *testing.T) {
	th, _ := summaryTestThread(t)
	p, f := cacheTestProvider(t, th)
	ctx := context.Background()
	first, err := p.Summary(ctx, th.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.LastMessage != "テストが成功しました" || first.CompletionRevision != completionRevision(lastTurn(th)) || first.Context == nil || first.Context.UsedTokens != 10000 {
		t.Fatalf("summary %+v", first)
	}
	for i := 0; i < 3; i++ {
		next, err := p.Summary(ctx, th.ID, nil)
		if err != nil || next.CompletionRevision != first.CompletionRevision {
			t.Fatalf("unchanged %+v %v", next, err)
		}
	}
	if historyCalls(f) != 1 {
		t.Fatal("repeated full history", f.callList())
	}
	// Token accounting does not change finished output or require another RPC.
	appendRollout(t, th.Path, `{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":11000},"total_token_usage":{"input_tokens":13000,"output_tokens":600},"model_context_window":200000}}}`+"\n")
	next, err := p.Summary(ctx, th.ID, nil)
	if err != nil || next.Context.UsedTokens != 11000 || next.CompletionRevision != first.CompletionRevision || historyCalls(f) != 1 {
		t.Fatalf("tokens %+v %v %v", next, err, f.callList())
	}
	// A background result changes an existing item, not the last assistant text.
	th.Turns[0].Items[1] = json.RawMessage(`{"type":"commandExecution","id":"cmd-1","status":"completed","aggregatedOutput":"background result","exitCode":0}`)
	setCacheThread(f, th)
	appendRollout(t, th.Path, `{"type":"event_msg","payload":{"type":"item_completed","turn_id":"turn-1","item":{"type":"CommandExecution","id":"cmd-1","status":"completed","aggregated_output":"background result"}}}`+"\n")
	next, err = p.Summary(ctx, th.ID, nil)
	if err != nil || next.CompletionRevision == first.CompletionRevision || next.CompletionRevision != completionRevision(lastTurn(th)) || historyCalls(f) != 2 {
		t.Fatalf("background %+v %v %v", next, err, f.callList())
	}
	if _, err = p.Summary(ctx, th.ID, nil); err != nil || historyCalls(f) != 2 {
		t.Fatal("repeated background reconciliation", err)
	}
}

func Test同じ終了ターンのログ書換えと置換で通知の版を再照合する(t *testing.T) {
	th, raw := summaryTestThread(t)
	p, f := cacheTestProvider(t, th)
	ctx := context.Background()
	first, err := p.Summary(ctx, th.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i, output := range []string{"ng", "xx"} {
		th.Turns[0].Items[1] = json.RawMessage(`{"type":"commandExecution","id":"cmd-1","status":"completed","aggregatedOutput":"` + output + `","exitCode":0}`)
		setCacheThread(f, th)
		// Preserve the turn ID, record count, and file size while changing output.
		updated := strings.Replace(string(raw), `"aggregated_output":"ok"`, `"aggregated_output":"`+output+`"`, 1)
		if i == 0 {
			if err := os.WriteFile(th.Path, []byte(updated), 0600); err != nil {
				t.Fatal(err)
			}
			future := time.Now().Add(time.Second)
			if err := os.Chtimes(th.Path, future, future); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.WriteFile(th.Path+".new", []byte(updated), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(th.Path+".new", th.Path); err != nil {
				t.Fatal(err)
			}
		}
		next, err := p.Summary(ctx, th.ID, nil)
		if err != nil || next.CompletionRevision == first.CompletionRevision || next.CompletionRevision != completionRevision(lastTurn(th)) {
			t.Fatalf("rewritten completion: %+v, %v", next, err)
		}
		first = next
	}
}

func Test同一ターンの完了イベントで結果が更新されたら通知の版を再照合する(t *testing.T) {
	th, _ := summaryTestThread(t)
	p, f := cacheTestProvider(t, th)
	ctx := context.Background()
	first, err := p.Summary(ctx, th.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	th.Turns[0].Items = append(th.Turns[0].Items, json.RawMessage(`{"type":"agentMessage","id":"background-answer","text":"バックグラウンド処理も完了しました"}`))
	setCacheThread(f, th)
	appendRollout(t, th.Path, `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1","last_agent_message":"バックグラウンド処理も完了しました"}}`+"\n")
	next, err := p.Summary(ctx, th.ID, nil)
	if err != nil || next.CompletionRevision == first.CompletionRevision || next.CompletionRevision != completionRevision(lastTurn(th)) || historyCalls(f) != 2 {
		t.Fatalf("updated completion: %+v, %v, %v", next, err, f.callList())
	}
}

func Test実行中の一覧は巨大なツール出力で全履歴を取得しない(t *testing.T) {
	th, _ := summaryTestThread(t)
	th.Turns[0].Status = "inProgress"
	os.WriteFile(th.Path, []byte(`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`+"\n"), 0600)
	p, f := cacheTestProvider(t, th)
	raw := []byte(`{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","aggregated_output":"` + strings.Repeat("x", 4<<20) + `"}}}`)
	appendRollout(t, th.Path, string(raw)+"\n")
	sum, err := p.Summary(context.Background(), th.ID, nil)
	if err != nil || sum.CompletionRevision != "" || historyCalls(f) != 0 {
		t.Fatalf("running %+v %v %v", sum, err, f.callList())
	}
	e := p.summaryEntries[th.Path]
	if e.offset != int64(len(raw)+1)+int64(len(`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}`)+1) || e.state.asyncBytes != 0 {
		t.Fatal("large record retained or not consumed")
	}
}

func Test要約は未完の行を再試行しファイル置換と縮小でリセットする(t *testing.T) {
	th, raw := summaryTestThread(t)
	p, _ := cacheTestProvider(t, th)
	ctx := context.Background()
	state, err := p.rolloutSummary(ctx, th.Path)
	if err != nil || state.status != "completed" {
		t.Fatal(state, err)
	}
	partial := `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}`
	appendRollout(t, th.Path, partial[:30])
	state, err = p.rolloutSummary(ctx, th.Path)
	if err != nil || state.turnID != "turn-1" {
		t.Fatal("partial consumed", state, err)
	}
	appendRollout(t, th.Path, partial[30:]+"\n")
	state, err = p.rolloutSummary(ctx, th.Path)
	if err != nil || state.turnID != "turn-2" || state.status != "inProgress" {
		t.Fatal("partial not retried", state, err)
	}
	os.WriteFile(th.Path, raw, 0600)
	state, err = p.rolloutSummary(ctx, th.Path)
	if err != nil || state.turnID != "turn-1" {
		t.Fatal("truncate", state, err)
	}
	tmp := th.Path + ".new"
	os.WriteFile(tmp, []byte(partial+"\n"), 0600)
	os.Rename(tmp, th.Path)
	state, err = p.rolloutSummary(ctx, th.Path)
	if err != nil || state.lastText != "" || state.tokens != nil {
		t.Fatal("replace", state, err)
	}
}

func Test圧縮後の要約は一度照合してから増分読み取りに戻る(t *testing.T) {
	th, raw := summaryTestThread(t)
	prefix := []byte(`{"type":"compacted","payload":{"message":"compacted"}}` + "\n")
	os.WriteFile(th.Path, append(prefix, raw...), 0600)
	p, f := cacheTestProvider(t, th)
	ctx := context.Background()
	first, err := p.Summary(ctx, th.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Summary(ctx, th.ID, nil)
	if err != nil || historyCalls(f) != 1 || first.CompletionRevision != second.CompletionRevision {
		t.Fatal("compaction not reconciled", err, f.callList())
	}
}

func Test履歴不変の要約でも端末のPlan確認と質問キューを確認する(t *testing.T) {
	th, _ := summaryTestThread(t)
	p, f := cacheTestProvider(t, th)
	ctx := context.Background()
	appendRollout(t, th.Path, `{"type":"event_msg","payload":{"type":"item_completed","item":{"type":"Plan","id":"plan-1","text":"plan"}}}`+"\n")
	th.Turns[0].Items = append(th.Turns[0].Items, json.RawMessage(`{"type":"plan","id":"plan-1","text":"plan"}`))
	setCacheThread(f, th)
	term := &asyncTerminal{screen: planPromptScreen}
	p.term = term
	sum, err := p.Summary(ctx, th.ID, &providers.Live{PaneID: "pane"})
	if err != nil || sum.Pending != model.InteractionApproval {
		t.Fatalf("plan %+v %v", sum, err)
	}
	term.screen = "normal composer"
	sum, err = p.Summary(ctx, th.ID, &providers.Live{PaneID: "pane"})
	if err != nil || sum.Pending != "" || historyCalls(f) != 1 {
		t.Fatalf("closed %+v %v %v", sum, err, f.callList())
	}
}

func Test要約と詳細の並行取得でキャッシュを破壊しない(t *testing.T) {
	th, _ := summaryTestThread(t)
	p, _ := cacheTestProvider(t, th)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 3; j++ {
				if _, err := p.Summary(ctx, th.ID, nil); err != nil {
					t.Error(err)
				}
				if _, _, err := p.Conversation(ctx, th.ID, nil); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	d := newDaemonConn(deadletter.Nop{})
	d.c = p.reader
	p.daemon = d
	d.Notification("turn/started", json.RawMessage(`{"threadId":"`+th.ID+`","turn":{"id":"new-turn","status":"inProgress"}}`))
	sum, err := p.Summary(ctx, th.ID, nil)
	if err != nil || sum.CompletionRevision != "" {
		t.Fatalf("buffered turn start %+v %v", sum, err)
	}
}

func Test巻き戻しイベントはファイル変更前でも要約を再照合する(t *testing.T) {
	th, _ := summaryTestThread(t)
	p, f := cacheTestProvider(t, th)
	d := newDaemonConn(deadletter.Nop{})
	d.c = p.reader
	p.daemon = d
	ctx := context.Background()
	if _, err := p.Summary(ctx, th.ID, nil); err != nil {
		t.Fatal(err)
	}
	th.Turns = []Turn{{ID: "reverted-turn", Status: "completed", Items: []json.RawMessage{json.RawMessage(`{"type":"agentMessage","id":"old-answer","text":"巻き戻した結果"}`)}}}
	setCacheThread(f, th)
	d.Notification("thread/reverted", json.RawMessage(`{"threadId":"`+th.ID+`"}`))
	sum, err := p.Summary(ctx, th.ID, nil)
	if err != nil || sum.LastMessage != "巻き戻した結果" || sum.CompletionRevision != completionRevision(lastTurn(th)) || historyCalls(f) != 2 {
		t.Fatalf("revert %+v %v %v", sum, err, f.callList())
	}
	sum, err = p.Summary(ctx, th.ID, nil)
	if err != nil || sum.LastMessage != "巻き戻した結果" || historyCalls(f) != 2 {
		t.Fatalf("revert cache %+v %v %v", sum, err, f.callList())
	}
}

func Test質問キューの投影は回答と画面上の解消を反映する(t *testing.T) {
	th, _ := loadThread(t, "async_reply.json")
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	th.Path = path
	var records strings.Builder
	records.WriteString(`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}` + "\n")
	for _, it := range th.Turns[0].Items[:1] {
		var compact bytes.Buffer
		json.Compact(&compact, it)
		records.WriteString(`{"type":"event_msg","payload":{"type":"item_completed","item":` + compact.String() + `}}` + "\n")
	}
	os.WriteFile(path, []byte(records.String()), 0600)
	p, f := cacheTestProvider(t, th)
	term := &asyncTerminal{screen: collapsedAsync}
	p.term = term
	live := &providers.Live{PaneID: "pane"}
	ctx := context.Background()
	sum, err := p.Summary(ctx, th.ID, live)
	if err != nil || sum.Pending != model.InteractionQuestions || historyCalls(f) != 0 {
		t.Fatalf("queued %+v %v %v", sum, err, f.callList())
	}
	term.screen = "normal composer"
	sum, err = p.Summary(ctx, th.ID, live)
	if err != nil || sum.Pending != "" || historyCalls(f) != 0 {
		t.Fatalf("dismissed %+v %v", sum, err)
	}
	var compact bytes.Buffer
	json.Compact(&compact, th.Turns[0].Items[1])
	appendRollout(t, path, `{"type":"event_msg","payload":{"type":"item_completed","item":`+compact.String()+`}}`+"\n")
	term.screen = collapsedAsync
	sum, err = p.Summary(ctx, th.ID, live)
	if err != nil || sum.Pending != "" || historyCalls(f) != 0 {
		t.Fatalf("answered %+v %v", sum, err)
	}
}
