package translation

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// ConversationReader supplies original provider messages, never display copies.
type ConversationReader interface {
	Conversation(context.Context, string) (model.Session, []model.Message, error)
}

func (s *Service) conversation(ctx context.Context, sessionID string) []model.Message {
	if s.conversations == nil || sessionID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, messages, err := s.conversations.Conversation(ctx, sessionID)
	if err != nil {
		return nil // Optional context must not prevent sending or notification delivery.
	}
	return messages
}

// contextBefore is deterministic at the source position: later conversation and
// completed translations cannot change an older report's cache key. At most two
// nearby prose blocks are retained, with bounded scanning and UTF-8-safe excerpts.
func contextBefore(messages []model.Message, messageIndex, blockIndex int) string {
	var excerpts []string
	start := min(messageIndex, len(messages)-1)
	for i := start; i >= max(0, start-63) && len(excerpts) < 2; i-- {
		m := messages[i]
		if m.Streaming || m.Queued || (m.Role != model.RoleUser && m.Role != model.RoleAssistant) {
			continue
		}
		end := len(m.Blocks)
		if i == messageIndex {
			end = min(end, blockIndex)
		}
		for b := end - 1; b >= 0 && len(excerpts) < 2; b-- {
			block := m.Blocks[b]
			if block.Type != model.BlockText || (m.Role == model.RoleAssistant && !block.Report) {
				continue
			}
			text := block.Text
			if block.OriginalText != "" {
				text = block.OriginalText
			}
			if text = contextProse(text); text != "" {
				excerpts = append(excerpts, string(m.Role)+": "+text)
			}
		}
	}
	if len(excerpts) == 0 {
		return ""
	}
	if len(excerpts) == 2 {
		excerpts[0], excerpts[1] = excerpts[1], excerpts[0]
	}
	return "Previous conversation (reference only):\n" + strings.Join(excerpts, "\n")
}

func contextProse(text string) string {
	// Avoid copying tool-like JSON or fenced/indented code into reference prose.
	trimmed := strings.TrimSpace(text)
	if (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Valid([]byte(trimmed)) {
		return ""
	}
	var lines []string
	var fence string
	for line := range strings.SplitSeq(text, "\n") {
		t := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(t, fence) && strings.Trim(t, string(fence[0])) == "" {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = t[:len(t)-len(strings.TrimLeft(t, t[:1]))]
			continue
		}
		if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			continue
		}
		if t != "" {
			lines = append(lines, t)
		}
	}
	runes := []rune(strings.Join(lines, "\n"))
	if len(runes) > 800 {
		// Preserve both the subject and the final proposal in a long response.
		return string(runes[:300]) + "\n[…]\n" + string(runes[len(runes)-500:])
	}
	return string(runes)
}
