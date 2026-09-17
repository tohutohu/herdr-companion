package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// Codex reports token counts only to the client that owns a turn, so the
// app-server never hands them to us. It does write them to the thread's
// rollout file after every turn, and thread/list and thread/read both give us
// that path, so the latest count is read from the end of that file.

// rolloutTail is how much of the end of a rollout file is scanned. A token
// count is written after every turn, so the newest one sits near the end.
const rolloutTail = 256 << 10

// rolloutLine is the part of a rollout record we read. Unlike the app-server
// protocol, rollout payloads are snake_case.
type rolloutLine struct {
	Payload struct {
		Type string `json:"type"`
		Info *struct {
			// LastTokenUsage is the newest request's accounting; its total is
			// what the conversation currently occupies.
			LastTokenUsage struct {
				TotalTokens int64 `json:"total_tokens"`
			} `json:"last_token_usage"`
			ModelContextWindow int64 `json:"model_context_window"`
		} `json:"info"`
	} `json:"payload"`
}

// contextFromRollout returns how full the thread's context window is, or nil
// when the file is missing or has no usable token count (an interrupted turn
// records one without an info block).
func contextFromRollout(path string) *model.ContextUsage {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	offset := max(st.Size()-rolloutTail, 0)
	buf := make([]byte, st.Size()-offset)
	if _, err := f.ReadAt(buf, offset); err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	if offset > 0 {
		// The window starts mid-record; drop that partial line.
		_, buf, _ = bytes.Cut(buf, []byte("\n"))
	}
	lines := bytes.Split(buf, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		var l rolloutLine
		if json.Unmarshal(lines[i], &l) != nil || l.Payload.Type != "token_count" || l.Payload.Info == nil {
			continue
		}
		if u := model.NewContextUsage(l.Payload.Info.LastTokenUsage.TotalTokens, l.Payload.Info.ModelContextWindow); u != nil {
			return u
		}
	}
	return nil
}
