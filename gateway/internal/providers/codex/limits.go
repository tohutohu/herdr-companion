package codex

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

const reserveWindowKey = "gpt-reserve"

// ReadReserveWindow reads the separate Luna Reserve quota from Codex's
// rate-limit snapshot. The normal Codex quota is still supplied by CodexBar;
// this method only returns the additional gpt-reserve window when present.
func (p *Provider) ReadReserveWindow(ctx context.Context) (*model.UsageWindow, error) {
	c, err := p.client(ctx)
	if err != nil {
		return nil, err
	}
	var response rateLimitsResponse
	if err := c.call(ctx, "account/rateLimits/read", map[string]any{
		"supportsLunaReserve": true,
	}, &response); err != nil {
		return nil, err
	}

	byID := response.RateLimitsByLimitID
	if len(byID) == 0 {
		byID = response.RateLimitsByLimitIDSnake
	}
	keys := make([]string, 0, len(byID))
	for key := range byID {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		snapshot := byID[key]
		if window := reserveWindow(key, &snapshot); window != nil {
			return window, nil
		}
	}
	if window := reserveWindow("", response.RateLimits); window != nil {
		return window, nil
	}
	return nil, nil
}

type rateLimitsResponse struct {
	RateLimits               *rateLimitSnapshot           `json:"rateLimits"`
	RateLimitsByLimitID      map[string]rateLimitSnapshot `json:"rateLimitsByLimitId"`
	RateLimitsByLimitIDSnake map[string]rateLimitSnapshot `json:"rate_limits_by_limit_id"`
}

type rateLimitSnapshot struct {
	LimitID         string           `json:"limitId"`
	LimitIDSnake    string           `json:"limit_id"`
	LimitName       string           `json:"limitName"`
	LimitNameSnake  string           `json:"limit_name"`
	NormalModelSlug string           `json:"normalModelSlug"`
	Primary         *rateLimitWindow `json:"primary"`
	Secondary       *rateLimitWindow `json:"secondary"`
}

type rateLimitWindow struct {
	UsedPercent             float64 `json:"usedPercent"`
	UsedPercentSnake        float64 `json:"used_percent"`
	WindowDurationMins      int     `json:"windowDurationMins"`
	WindowDurationMinsSnake int     `json:"window_duration_mins"`
	WindowMinutes           int     `json:"windowMinutes"`
	WindowMinutesSnake      int     `json:"window_minutes"`
	ResetsAt                int64   `json:"resetsAt"`
	ResetsAtSnake           int64   `json:"resets_at"`
}

func reserveWindow(key string, snapshot *rateLimitSnapshot) *model.UsageWindow {
	if snapshot == nil || !isReserveLimit(key, *snapshot) {
		return nil
	}
	window := snapshot.Primary
	if window == nil {
		window = snapshot.Secondary
	}
	if window == nil {
		return nil
	}
	minutes := window.minutes()
	label := windowLabel(minutes)
	if label == "" {
		label = reserveWindowKey
	}
	percent := window.UsedPercent
	if percent == 0 {
		percent = window.UsedPercentSnake
	}
	resetsAt := window.ResetsAt
	if resetsAt == 0 {
		resetsAt = window.ResetsAtSnake
	}
	var reset *time.Time
	if resetsAt > 0 {
		t := time.Unix(resetsAt, 0).UTC()
		reset = &t
	}
	scope := firstNonEmpty(snapshot.LimitName, snapshot.LimitNameSnake, reserveWindowKey)
	return &model.UsageWindow{
		Key:           reserveWindowKey,
		Label:         label,
		Scope:         scope,
		UsedPercent:   clampPercent(percent),
		WindowMinutes: minutes,
		ResetsAt:      reset,
	}
}

func isReserveLimit(key string, snapshot rateLimitSnapshot) bool {
	return strings.EqualFold(key, reserveWindowKey) ||
		strings.EqualFold(key, "base_model_inference") ||
		strings.EqualFold(firstNonEmpty(snapshot.LimitName, snapshot.LimitNameSnake), reserveWindowKey)
}

func (w rateLimitWindow) minutes() int {
	for _, minutes := range []int{w.WindowDurationMins, w.WindowDurationMinsSnake, w.WindowMinutes, w.WindowMinutesSnake} {
		if minutes > 0 {
			return minutes
		}
	}
	return 0
}

func windowLabel(minutes int) string {
	switch {
	case minutes <= 0:
		return ""
	case minutes%(60*24) == 0:
		return strconv.Itoa(minutes/(60*24)) + "d"
	case minutes%60 == 0:
		return strconv.Itoa(minutes/60) + "h"
	default:
		return strconv.Itoa(minutes) + "m"
	}
}

func clampPercent(value float64) int {
	n := int(math.Round(value))
	if n < 0 {
		return 0
	}
	if n > 100 {
		return 100
	}
	return n
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
