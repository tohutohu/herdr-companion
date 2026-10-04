package codex

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"reflect"
	"strings"
	"testing"

	goccyjson "github.com/goccy/go-json"
)

// Model a long thread with large tool results, the expensive part of polling.
// Maps keep the benchmark input independent of generated codecs on our types.
func benchmarkThreadJSON(b *testing.B) ([]byte, []byte, []byte) {
	return benchmarkThreadWithOutput(b, strings.Repeat("test output line\n", 4096))
}

func benchmarkThreadWithOutput(b *testing.B, output string) ([]byte, []byte, []byte) {
	b.Helper()
	it := map[string]any{"id": "command-1", "type": "commandExecution", "command": "go test ./...",
		"aggregatedOutput": output, "exitCode": 0, "status": "completed"}
	turns := make([]any, 32)
	for i := range turns {
		turns[i] = map[string]any{"id": "turn-1", "status": "completed", "items": []any{it}}
	}
	thread := map[string]any{"id": "thread-000001", "cwd": "/work/app", "model": "gpt-6.1-sol", "turns": turns}
	marshal := func(v any) []byte {
		b.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			b.Fatal(err)
		}
		return raw
	}
	return marshal(map[string]any{"id": 1, "result": map[string]any{"thread": thread}}), marshal(thread), marshal(it)
}

func BenchmarkCodexJSON(b *testing.B) {
	wire, thread, rawItem := benchmarkThreadJSON(b)
	benchmarkCodexDecoders(b, wire, thread, rawItem)
}

func BenchmarkCodexJSONUnicode(b *testing.B) {
	wire, thread, rawItem := benchmarkThreadWithOutput(b, strings.Repeat("テスト出力: \"日本語\" / path\\file\t🙂\n", 2048))
	benchmarkCodexDecoders(b, wire, thread, rawItem)
}

func benchmarkCodexDecoders(b *testing.B, wire, thread, rawItem []byte) {
	for _, tc := range []struct {
		name string
		raw  []byte
		typ  reflect.Type
	}{
		{"RPC", wire, reflect.TypeFor[wireMessage]()},
		{"Thread", thread, reflect.TypeFor[Thread]()},
		{"Item", rawItem, reflect.TypeFor[item]()},
		{"Header", rawItem, reflect.TypeFor[itemHead]()},
		{"Questions", rawItem, reflect.TypeFor[asyncHistoryItem]()},
	} {
		for _, codec := range []struct {
			name   string
			decode func([]byte, any) error
		}{
			{"v1", json.Unmarshal},
			{"v2", func(raw []byte, out any) error { return jsonv2.Unmarshal(raw, out) }},
			{"goccy", goccyjson.Unmarshal},
		} {
			b.Run(tc.name+"/"+codec.name, func(b *testing.B) {
				b.SetBytes(int64(len(tc.raw)))
				b.ReportAllocs()
				for b.Loop() {
					value := reflect.New(tc.typ).Interface()
					if err := codec.decode(tc.raw, value); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// Exercise the production passes, including lightweight question projections.
func BenchmarkCodexHistory(b *testing.B) {
	_, raw, _ := benchmarkThreadJSON(b)
	var th Thread
	if err := jsonv2.Unmarshal(raw, &th); err != nil {
		b.Fatal(err)
	}
	th.Turns = append(th.Turns, Turn{Items: []json.RawMessage{
		json.RawMessage(`{"type":"agentMessage","id":"last","text":"テストが完了しました。"}`),
	}})
	b.ReportAllocs()
	for b.Loop() {
		if got := ConvertThread(&th, convertOptions{}); len(got) != 33 {
			b.Fatal("lost messages")
		}
		if got := pendingAsyncQuestions(&th); len(got) != 0 {
			b.Fatal("unexpected pending questions")
		}
		if got := lastThreadText(&th); got != "テストが完了しました。" {
			b.Fatal("lost last text")
		}
	}
}
