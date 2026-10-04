package codex

import (
	"bytes"
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	goccyjson "github.com/goccy/go-json"
)

func compareCodexDecoders(t *testing.T, raw []byte, typ reflect.Type) {
	t.Helper()
	want := reflect.New(typ).Interface()
	if err := jsonv2.Unmarshal(raw, want); err != nil {
		t.Fatal(err)
	}
	// Decode from a disposable transport buffer. Returned strings and RawMessage
	// must survive both input reuse and a subsequent decode on the same codec.
	scratch := bytes.Clone(raw)
	got := reflect.New(typ).Interface()
	if err := goccyjson.Unmarshal(scratch, got); err != nil {
		t.Fatal(err)
	}
	clear(scratch)
	if err := goccyjson.Unmarshal(raw, reflect.New(typ).Interface()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v decoded differently or retained reused input", typ)
	}
}

func TestJSON復号は既存Codex履歴と承認と画像の値を維持する(t *testing.T) {
	for _, name := range []string{"normal.json", "rich.json", "async_question.json", "async_multi.json", "async_reply.json"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(testdata, name))
			if err != nil {
				t.Fatal(err)
			}
			var r struct {
				Thread Thread `json:"thread"`
			}
			if err := jsonv2.Unmarshal(raw, &r); err != nil {
				t.Fatal(err)
			}
			thread, err := json.Marshal(r.Thread)
			if err != nil {
				t.Fatal(err)
			}
			compareCodexDecoders(t, thread, reflect.TypeFor[Thread]())
			wire, err := json.Marshal(map[string]any{"id": 1, "result": r})
			if err != nil {
				t.Fatal(err)
			}
			compareCodexDecoders(t, wire, reflect.TypeFor[wireMessage]())
			for _, turn := range r.Thread.Turns {
				for _, rawItem := range turn.Items {
					for _, typ := range []reflect.Type{reflect.TypeFor[itemHead](), reflect.TypeFor[asyncHistoryItem](), reflect.TypeFor[item]()} {
						compareCodexDecoders(t, rawItem, typ)
					}
				}
			}
		})
	}
}

func TestJSON復号は壊れたRPCとitemを受理しない(t *testing.T) {
	for _, raw := range []string{`{"id":`, `{"id":1,"result":{"thread":]}}`, `{"id":"a","type":"agentMessage","text":"unterminated}`, `{"id":"a","type":"agentMessage"} trailing`, `{"type":123}`} {
		for _, typ := range []reflect.Type{reflect.TypeFor[wireMessage](), reflect.TypeFor[item]()} {
			wantErr := jsonv2.Unmarshal([]byte(raw), reflect.New(typ).Interface())
			gotErr := goccyjson.Unmarshal([]byte(raw), reflect.New(typ).Interface())
			if (wantErr == nil) != (gotErr == nil) {
				t.Fatalf("%v: error acceptance differs for %q: v2=%v goccy=%v", typ, raw, wantErr, gotErr)
			}
		}
	}
}

func TestRPC受信で結果内の重複キーと不正UTF8を拒否する(t *testing.T) {
	for _, bad := range []string{
		`{"id":1,"result":{"text":"bad","text":"duplicate"}}`,
		"{\"id\":1,\"result\":{\"text\":\"\xff\"}}",
	} {
		pt := &pipeTransport{in: make(chan []byte, 16), out: make(chan []byte, 16), closed: make(chan struct{})}
		c := newRPCClient(pt, nil)
		t.Cleanup(c.close)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var result struct {
			Text string `json:"text"`
		}
		done := make(chan error, 1)
		go func() { done <- c.call(ctx, "thread/read", map[string]any{}, &result) }()
		select {
		case <-pt.out:
		case <-ctx.Done():
			t.Fatal("no RPC request")
		}
		pt.in <- []byte(bad)
		pt.in <- []byte(`{"id":1,"result":{"text":"valid"}}`)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if result.Text != "valid" {
			t.Fatal("invalid RPC payload passed boundary validation")
		}
	}
}
