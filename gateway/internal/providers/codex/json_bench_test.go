package codex

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"reflect"
	"strings"
	"testing"
)

// Model a long thread with large tool results, the expensive part of polling.
// Maps keep the benchmark input independent of generated codecs on our types.
func benchmarkThreadJSON(b *testing.B) ([]byte, []byte, []byte) {
	b.Helper()
	it := map[string]any{"id": "command-1", "type": "commandExecution", "command": "go test ./...",
		"aggregatedOutput": strings.Repeat("test output line\n", 4096), "exitCode": 0, "status": "completed"}
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
