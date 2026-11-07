package translation

import (
	"context"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/notifications"
)

// OutputEnabled requires an explicit translation choice.
func (s *Service) OutputEnabled(mode ...*bool) bool {
	return modeEnabled(mode)
}

// Notification translates before payload truncation and device fan-out. It uses
// the same cache as the conversation, so a report translated during the turn is
// available without running inference again. Failures retain readable originals.
func (s *Service) Notification(ctx context.Context, sessionID string, content notifications.Content, mode ...*bool) notifications.Content {
	if !s.OutputEnabled(mode...) {
		return content
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	display := func(text, reference string) string {
		if !hasLatin(text) {
			return text
		}
		translated, err := s.translate(ctx, text, "ja", reference)
		if err != nil {
			return text
		}
		return translated
	}
	content.Title = display(content.Title, "")
	content.Body = display(content.Body, "")
	if content.Report != nil {
		messages := s.conversation(ctx, sessionID)
		position := -1
		for i := range messages {
			if content.Report.ID != "" && messages[i].ID == content.Report.ID {
				position = i
				break
			}
		}
		report := *content.Report
		report.Blocks = append([]model.Block(nil), report.Blocks...)
		// The notification watcher trims and merges text blocks into one. Recover
		// their original boundaries before translating so each uses the same
		// source text and context as Messages, then merge the display copies again.
		if position >= 0 && len(report.Blocks) == 1 {
			merged, ok := notifications.LastReport(messages[position : position+1])
			if ok && merged.Blocks[0].Text == report.Blocks[0].Text {
				var parts []string
				for i, block := range messages[position].Blocks {
					if block.Type == model.BlockText && strings.TrimSpace(block.Text) != "" {
						parts = append(parts, strings.TrimSpace(display(block.Text, contextBefore(messages, position, i))))
					}
				}
				if translated := strings.Join(parts, "\n\n"); translated != report.Blocks[0].Text {
					report.Blocks[0].OriginalText, report.Blocks[0].Text = report.Blocks[0].Text, translated
				}
				content.Report = &report
				return content
			}
		}
		for i := range report.Blocks {
			block := &report.Blocks[i]
			if block.Type == model.BlockText {
				reference := ""
				if position >= 0 && i < len(messages[position].Blocks) && messages[position].Blocks[i].Text == block.Text {
					reference = contextBefore(messages, position, i)
				}
				if translated := display(block.Text, reference); translated != block.Text {
					block.OriginalText, block.Text = block.Text, translated
				}
			}
		}
		content.Report = &report
	}
	return content
}
