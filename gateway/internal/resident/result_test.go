package resident

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParseResult(t *testing.T) {
	valid := mustJSON(result(OutcomeChanged, "CIが復旧", withKey("ci:1"), withSet("seen", `["a"]`)))
	t.Run("有効な結果を受け付ける", func(t *testing.T) {
		r, err := ParseResult([]byte(valid))
		if err != nil || r.Outcome != OutcomeChanged || *r.Notification.Key != "ci:1" || len(r.StatePatch) != 1 {
			t.Fatalf("%+v %v", r, err)
		}
	})
	t.Run("コードフェンスで囲まれた結果も読む", func(t *testing.T) {
		if _, err := ParseResult([]byte("```json\n" + valid + "\n```")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("スキーマのJSONSchema自体が正しいJSON", func(t *testing.T) {
		var v map[string]any
		if err := json.Unmarshal(ResultSchema, &v); err != nil {
			t.Fatal(err)
		}
	})
	bad := []struct {
		name string
		raw  string
		want string
	}{
		{"空の結果を拒否する", "", "no result"},
		{"JSONでない本文を拒否する", "変化はありません", "not valid JSON"},
		{"未知の項目を拒否する", strings.Replace(valid, `"summary"`, `"extra":1,"summary"`, 1), "unknown field"},
		{"必須項目の欠落を拒否する", `{"schemaVersion":1,"outcome":"changed","summary":"x"}`, "lacks"},
		{"未知のoutcomeを拒否する", strings.Replace(valid, `"changed"`, `"ok"`, 1), "unknown outcome"},
		{"空のsummaryを拒否する", strings.Replace(valid, `"CIが復旧"`, `" "`, 1), "summary is empty"},
		{"不正なstatePatchのキーを拒否する", strings.Replace(valid, `"key":"seen"`, `"key":"a b"`, 1), "must match"},
		{"JSONでないstatePatchの値を拒否する", strings.Replace(valid, `"value":"[\"a\"]"`, `"value":"[a"`, 1), "JSON text"},
		{"RFC3339でないnextWakeAtを拒否する", strings.Replace(valid, `"nextWakeAt":null`, `"nextWakeAt":"明日"`, 1), "RFC 3339"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseResult([]byte(c.raw))
			if !errors.Is(err, ErrContract) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestApplyPatch(t *testing.T) {
	v := func(s string) *string { return &s }
	t.Run("ロック中のキーは更新せず報告する", func(t *testing.T) {
		state := map[string]json.RawMessage{"a": json.RawMessage(`1`), "b": json.RawMessage(`2`)}
		out, skipped, err := ApplyPatch(state, []PatchOp{{Op: "set", Key: "a", Value: v("10")}, {Op: "remove", Key: "b"}, {Op: "set", Key: "c", Value: v(`{"x": 1}`)}}, map[string]bool{"b": true})
		if err != nil || string(out["a"]) != "10" || string(out["b"]) != "2" || string(out["c"]) != `{"x":1}` || len(skipped) != 1 {
			t.Errorf("%v %v %v", out, skipped, err)
		}
	})
	t.Run("上限を超えるstatePatchは全体を拒否する", func(t *testing.T) {
		big := `"` + strings.Repeat("x", MaxStateBytes) + `"`
		if _, _, err := ApplyPatch(map[string]json.RawMessage{}, []PatchOp{{Op: "set", Key: "big", Value: &big}}, nil); err == nil {
			t.Error("accepted a state over the limit")
		}
	})
	t.Run("要約は8KiBで文字境界を保って切る", func(t *testing.T) {
		s, cut := FitSummary(strings.Repeat("あ", MaxSummaryBytes))
		if !cut || len(s) > MaxSummaryBytes || !strings.HasSuffix(s, "…") {
			t.Errorf("len=%d cut=%v", len(s), cut)
		}
	})
}

func Test結果のJSONをセッションで読める文章にする(t *testing.T) {
	t.Run("要約と報告と質問を並べる", func(t *testing.T) {
		got, ok := DisplayText([]byte(`{"schemaVersion":1,"outcome":"blocked","summary":"CIが失敗しています。","report":"## 詳細\n- test が落ちた","notification":{"requested":true,"key":"k"},"statePatch":[],"memorySummary":null,"nextWakeAt":null,"question":{"text":"再実行しますか？","options":["する","しない"]},"artifacts":[]}`))
		want := "CIが失敗しています。\n\n## 詳細\n- test が落ちた\n\n**質問:** 再実行しますか？\n- する\n- しない"
		if !ok || got != want {
			t.Errorf("got %q, %v\nwant %q", got, ok, want)
		}
	})
	t.Run("報告が空なら要約だけにしコードフェンスも外す", func(t *testing.T) {
		got, ok := DisplayText([]byte("```json\n{\"schemaVersion\":1,\"outcome\":\"unchanged\",\"summary\":\"変化なし\",\"report\":\"\"}\n```"))
		if !ok || got != "変化なし" {
			t.Errorf("got %q, %v", got, ok)
		}
	})
	t.Run("結果の形でない文章やJSONはそのままにする", func(t *testing.T) {
		for _, in := range []string{"直しました。", `{"summary":"x"}`, `{"schemaVersion":1,"outcome":"done","summary":"x"}`, ""} {
			if got, ok := DisplayText([]byte(in)); ok {
				t.Errorf("DisplayText(%q) = %q, want not a result", in, got)
			}
		}
	})
}
