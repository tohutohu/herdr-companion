package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/pricing"
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
		Type string     `json:"type"`
		Info *tokenInfo `json:"info"`
	} `json:"payload"`
}

type tokenInfo struct {
	// LastTokenUsage is the newest request's accounting; its total is
	// what the conversation currently occupies.
	LastTokenUsage struct {
		TotalTokens int64 `json:"total_tokens"`
	} `json:"last_token_usage"`
	// TotalTokenUsage is everything the thread has spent so far. Its
	// input side includes the cached tokens, which bill at a lower rate.
	TotalTokenUsage struct {
		InputTokens       int64 `json:"input_tokens"`
		CachedInputTokens int64 `json:"cached_input_tokens"`
		OutputTokens      int64 `json:"output_tokens"`
	} `json:"total_token_usage"`
	ModelContextWindow int64 `json:"model_context_window"`
}

// context is how full the thread's context window is.
func (i *tokenInfo) context() *model.ContextUsage {
	if i == nil {
		return nil
	}
	return model.NewContextUsage(i.LastTokenUsage.TotalTokens, i.ModelContextWindow)
}

// cost prices the thread's tokens. Codex keeps one running total for the
// whole thread rather than per model, so the model it runs on now prices all
// of it; switching models mid-thread skews the result.
func (i *tokenInfo) cost(modelID string) *model.Cost {
	if i == nil {
		return nil
	}
	u := i.TotalTokenUsage
	rates, ok := pricing.Lookup(modelID)
	if !ok || u.InputTokens+u.OutputTokens <= 0 {
		return nil
	}
	usd := rates.USD(pricing.Tokens{
		Input:     max(u.InputTokens-u.CachedInputTokens, 0),
		CacheRead: u.CachedInputTokens,
		Output:    u.OutputTokens,
	})
	return &model.Cost{USD: usd, Estimated: true}
}

// infoFromRollout returns the newest token accounting in the thread's rollout
// file, or nil when the file is missing or has no usable count (an
// interrupted turn records one without an info block).
func infoFromRollout(path string) *tokenInfo {
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
	var out *tokenInfo
	for i := len(lines) - 1; i >= 0; i-- {
		var l tokenInfo
		if !decodeTokenCount(lines[i], &l) {
			continue
		}
		if out == nil {
			out = &l
		}
		// An interrupted turn can leave a record without a context window or
		// without totals; the rest of them are on the records before it.
		if out.context() == nil && l.context() != nil {
			out.LastTokenUsage = l.LastTokenUsage
			out.ModelContextWindow = l.ModelContextWindow
		}
		if out.TotalTokenUsage.InputTokens == 0 && l.TotalTokenUsage.InputTokens > 0 {
			out.TotalTokenUsage = l.TotalTokenUsage
		}
		if out.context() != nil && out.TotalTokenUsage.InputTokens > 0 {
			break
		}
	}
	return out
}

func decodeTokenCount(line []byte, info *tokenInfo) bool {
	var l rolloutLine
	if json.Unmarshal(line, &l) != nil || l.Payload.Type != "token_count" || l.Payload.Info == nil {
		return false
	}
	*info = *l.Payload.Info
	return true
}
