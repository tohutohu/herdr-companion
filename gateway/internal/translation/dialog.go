package translation

import (
	"strings"
	"time"
	"unicode"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// cachedDisplayLocked only schedules display strings. Commands, IDs, decisions,
// previews and selection values never enter the translation queue.
func (s *Service) cachedDisplayLocked(text string, urgent bool) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	latin := false
	for _, r := range text {
		if unicode.In(r, unicode.Latin) {
			latin = true
			break
		}
	}
	if !latin {
		return text
	}
	key := translationKey(s.status.Model, "ja", text)
	v, ok := s.cache[key]
	if translated, hit := s.cacheGetLocked(key); hit {
		return translated
	}
	if !s.canTranslateLocked() || s.pending[key] || (ok && time.Now().Before(v.retry)) {
		return text
	}
	queue := s.queue
	if urgent {
		queue = s.priority
	}
	select {
	case queue <- job{key, text}:
		s.pending[key] = true
	default:
	}
	return text
}
func (s *Service) dialogLocked(ia *model.Interaction) *model.Interaction {
	translated := *ia
	translated.Translation = nil
	translated.Title = s.cachedDisplayLocked(ia.Title, true)
	// Approval detail may be executable code. Keep it exact and append only
	// the provider's separately identified human explanation in Japanese.
	if ia.Type == model.InteractionApproval {
		if ia.DetailProse != "" {
			reason := s.cachedDisplayLocked(ia.DetailProse, true)
			if reason != ia.DetailProse {
				translated.Detail = ia.Detail + "\n\n" + reason
			}
		}
	} else {
		translated.Detail = s.cachedDisplayLocked(ia.Detail, true)
	}
	translated.Questions = append([]model.Question(nil), ia.Questions...)
	changed := translated.Title != ia.Title || translated.Detail != ia.Detail
	for i := range translated.Questions {
		q := &translated.Questions[i]
		original := ia.Questions[i]
		q.Header = s.cachedDisplayLocked(q.Header, true)
		q.Question = s.cachedDisplayLocked(q.Question, true)
		q.OtherLabel = s.cachedDisplayLocked(q.OtherLabel, true)
		if q.Header != original.Header || q.Question != original.Question || q.OtherLabel != original.OtherLabel {
			changed = true
		}
		q.Options = append([]model.Option(nil), q.Options...)
		for j := range q.Options {
			option := &q.Options[j]
			raw := original.Options[j]
			option.Value = raw.Label
			option.Label = s.cachedDisplayLocked(raw.Label, true)
			option.Description = s.cachedDisplayLocked(raw.Description, true)
			if option.Label != raw.Label || option.Description != raw.Description {
				changed = true
			}
		}
	}
	copy := *ia
	if changed {
		copy.Translation = &translated
	}
	return &copy
}

// Session returns a display copy; provider titles and notification state stay unchanged.
func (s *Service) Session(session model.Session, mode ...*bool) model.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !modeEnabled(s.status.Output, mode) {
		return session
	}
	translated := s.cachedDisplayLocked(session.Title, false)
	if translated != session.Title {
		session.OriginalTitle, session.Title = session.Title, translated
	}
	return session
}
