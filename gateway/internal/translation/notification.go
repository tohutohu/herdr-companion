package translation

import (
	"context"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/notifications"
)

// OutputEnabled includes the per-session override; resident pushes use defaults.
func (s *Service) OutputEnabled(mode ...*bool) bool {
	return modeEnabled(s.Status().Output, mode)
}

// Notification translates before payload truncation and device fan-out. It uses
// the same cache as the conversation, so a report translated during the turn is
// available without running inference again. Failures retain readable originals.
func (s *Service) Notification(ctx context.Context, _ string, content notifications.Content, mode ...*bool) notifications.Content {
	if !s.OutputEnabled(mode...) {
		return content
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	display := func(text string) string {
		if !hasLatin(text) {
			return text
		}
		translated, err := s.Translate(ctx, text, "ja")
		if err != nil {
			return text
		}
		return translated
	}
	content.Title = display(content.Title)
	content.Body = display(content.Body)
	if content.Report != nil {
		report := *content.Report
		report.Blocks = append([]model.Block(nil), report.Blocks...)
		for i := range report.Blocks {
			block := &report.Blocks[i]
			if block.Type == model.BlockText {
				if translated := display(block.Text); translated != block.Text {
					block.OriginalText, block.Text = block.Text, translated
				}
			}
		}
		content.Report = &report
	}
	return content
}
