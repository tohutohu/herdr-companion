package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// A Plan mode turn: the agent asks, the user answers, the agent goes on.
var answeredRollout = []string{
	`{"timestamp":"2026-09-18T03:11:44.716Z","type":"turn_context","payload":{"turn_id":"turn1","collaboration_mode":{"mode":"plan","settings":{"model":"gpt-6-astra"}}}}`,
	`{"timestamp":"2026-09-18T03:11:44.720Z","type":"response_item","payload":{"type":"message","id":"msg_user","role":"user","content":[{"type":"input_text","text":"add divide"}]}}`,
	`{"timestamp":"2026-09-18T03:11:52.045Z","type":"response_item","payload":{"type":"function_call","id":"fc_1","name":"request_user_input","arguments":"{\"questions\":[{\"header\":\"Zero division\",\"id\":\"zero_division\",\"question\":\"How should divide(a, b) handle division by zero?\",\"options\":[{\"label\":\"Raise ZeroDivisionError (Recommended)\",\"description\":\"Use Python's standard behavior.\"},{\"label\":\"Return None\",\"description\":\"Return None when the divisor is zero.\"}]}]}","call_id":"call_1"}}`,
	`{"timestamp":"2026-09-18T03:14:41.819Z","type":"response_item","payload":{"type":"function_call_output","id":"fco_1","call_id":"call_1","output":"{\"answers\":{\"zero_division\":{\"answers\":[\"Return None\"]}}}"}}`,
	`{"timestamp":"2026-09-18T03:14:45.000Z","type":"response_item","payload":{"type":"message","id":"msg_agent","role":"assistant","content":[{"type":"output_text","text":"I'll check calc.py."}]}}`,
	`{"timestamp":"2026-09-18T03:15:00.000Z","type":"response_item","payload":{"type":"function_call","id":"fc_2","name":"request_user_input","arguments":"{\"questions\":[{\"id\":\"later\",\"question\":\"Still open?\",\"options\":[]}]}","call_id":"call_2"}}`,
}

func writeRollout(t *testing.T, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func Test回答済みの質問はrolloutから復元して回答直後の発言の前に置く(t *testing.T) {
	path := writeRollout(t, answeredRollout)
	th := &Thread{ID: "th1", Path: path, Turns: []Turn{{ID: "turn1", Status: "completed", Items: []json.RawMessage{
		json.RawMessage(`{"type":"userMessage","id":"u1","content":[{"type":"text","text":"add divide"}]}`),
		json.RawMessage(`{"type":"agentMessage","id":"msg_agent","text":"I'll check calc.py."}`),
		json.RawMessage(`{"type":"plan","id":"turn1-plan","text":"### Add divide"}`),
	}}}}
	msgs := ConvertThread(th, convertOptions{SessionID: "codex:th1", Answered: answeredInputs(path)})

	var ids []string
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	// The still-open second call has no answer yet and is not shown.
	if got := strings.Join(ids, ","); got != "u1,codex-answered:call_1,msg_agent,turn1-plan" {
		t.Fatalf("order = %s", got)
	}
	ia := msgs[1].Blocks[0].Interaction
	if ia.State != model.InteractionAnswered || ia.Answer != "Return None" || ia.Questions[0].Question != "How should divide(a, b) handle division by zero?" || len(ia.Questions[0].Options) != 2 {
		t.Errorf("answered card = %+v", ia)
	}
}

func Test回答後の発言がなければ質問はターンの最後に置く(t *testing.T) {
	path := writeRollout(t, answeredRollout[:4])
	th := &Thread{ID: "th1", Path: path, Turns: []Turn{
		{ID: "turn1", Status: "interrupted", Items: []json.RawMessage{json.RawMessage(`{"type":"userMessage","id":"u1","content":[{"type":"text","text":"add divide"}]}`)}},
		{ID: "turn2", Status: "completed", Items: []json.RawMessage{json.RawMessage(`{"type":"userMessage","id":"u2","content":[{"type":"text","text":"next"}]}`)}},
	}}
	msgs := ConvertThread(th, convertOptions{Answered: answeredInputs(path)})
	var ids []string
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	if got := strings.Join(ids, ","); got != "u1,codex-answered:call_1,u2" {
		t.Errorf("order = %s", got)
	}
}

func Test複数の質問への回答は質問ごとにまとめる(t *testing.T) {
	args := userInputArgs{}
	json.Unmarshal([]byte(`{"questions":[{"id":"a","question":"Language?","options":[{"label":"Go"}]},{"id":"b","question":"Name?"}]}`), &args)
	ia := answeredInteraction("call_9", args, `{"answers":{"a":{"answers":["Go"]},"b":{"answers":["calc"]}}}`)
	if ia.Answer != "Language? → Go\nName? → calc" || ia.Questions[1].Type != model.QuestionText {
		t.Errorf("answer = %q, questions = %+v", ia.Answer, ia.Questions)
	}
	if ia := answeredInteraction("call_9", args, `{"answers":{}}`); ia.Answer != "Not answered" {
		t.Errorf("empty answer = %q", ia.Answer)
	}
}

func Test回答履歴は追記分を読み未完の行と質問の状態を次回へ持ち越す(t *testing.T) {
	path := writeRollout(t, answeredRollout[:3])
	if got := answeredInputs(path); len(got) != 0 {
		t.Fatal(got)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	line := answeredRollout[3]
	if _, err := f.WriteString(line[:len(line)/2]); err != nil {
		t.Fatal(err)
	}
	if got := answeredInputs(path); len(got) != 0 {
		t.Fatal("partial answer was parsed")
	}
	if _, err := f.WriteString(line[len(line)/2:] + "\n"); err != nil {
		t.Fatal(err)
	}
	before := answeredInputs(path)
	if len(before) != 1 || before[0].Before != "" || before[0].Interaction.Answer != "Return None" {
		t.Fatal(before)
	}
	if _, err := f.WriteString(answeredRollout[4] + "\n"); err != nil {
		t.Fatal(err)
	}
	after := answeredInputs(path)
	if len(after) != 1 || after[0].Before != "msg_agent" || before[0].Before != "" {
		t.Fatal("append changed a previous snapshot or duplicated the answer")
	}
	if !reflect.DeepEqual(after, readAnsweredInputs(path)) {
		t.Fatal("incremental read differs from full read")
	}
	// A rewritten/truncated file must not retain earlier questions or offsets.
	if err := os.WriteFile(path, []byte(answeredRollout[0]+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := answeredInputs(path); len(got) != 0 {
		t.Fatal("truncated file retained answers")
	}
}

func BenchmarkAnsweredAppend(b *testing.B) {
	for _, incremental := range []bool{false, true} {
		name := "FullScan"
		if incremental {
			name = "Incremental"
		}
		b.Run(name, func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "rollout.jsonl")
			large := `{"type":"response_item","payload":{"type":"function_call_output","output":"` + strings.Repeat("x", 4<<20) + `"}}` + "\n"
			if err := os.WriteFile(path, []byte(large+strings.Join(answeredRollout[:4], "\n")+"\n"), 0600); err != nil {
				b.Fatal(err)
			}
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				b.Fatal(err)
			}
			defer f.Close()
			answeredInputs(path)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := f.WriteString(`{"type":"event_msg","payload":{"type":"token_count"}}` + "\n"); err != nil {
					b.Fatal(err)
				}
				var got []answeredInput
				if incremental {
					got = answeredInputs(path)
				} else {
					got = readAnsweredInputs(path)
				}
				if len(got) != 1 {
					b.Fatal("answer lost")
				}
			}
		})
	}
}
