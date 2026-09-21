package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers/claude"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers/codex"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers/devin"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers/opencode"
)

// replayers re-run a provider parser over a stored raw payload.
var replayers = map[string]func(raw []byte) ([]model.Message, []deadletter.Entry){
	"claude":   claude.Replay,
	"codex":    codex.Replay,
	"devin":    devin.Replay,
	"opencode": opencode.Replay,
}

func debugCmd(args []string) error {
	if len(args) != 2 || args[0] != "replay" {
		return fmt.Errorf("usage: herdr-mobile-gateway debug replay FILE")
	}
	f, err := os.Open(args[1])
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	line, fixed, still := 0, 0, 0
	for sc.Scan() {
		line++
		var e deadletter.Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			fmt.Fprintf(os.Stderr, "line %d: %v\n", line, err)
			continue
		}
		replay, ok := replayers[e.Provider]
		if !ok || len(e.Raw) == 0 {
			fmt.Printf("# line %d: %s/%s: no replayer\n", line, e.Provider, e.Kind)
			continue
		}
		msgs, errs := replay(e.Raw)
		status := "OK"
		if len(errs) > 0 {
			status = "STILL FAILING"
			still++
		} else {
			fixed++
		}
		fmt.Printf("# line %d: %s %s (%s) -> %s\n", line, e.Provider, e.Kind, e.Error, status)
		for _, de := range errs {
			fmt.Printf("#   %s: %s\n", de.Kind, de.Error)
		}
		enc.Encode(msgs)
	}
	fmt.Printf("# %d entries now parse cleanly, %d still fail\n", fixed, still)
	return sc.Err()
}
