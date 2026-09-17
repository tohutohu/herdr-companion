// Package usage reports how much of the Claude / Codex subscription limits
// is left.
//
// Neither agent exposes this on its own CLI, and reading their credentials
// here would duplicate two OAuth flows, so the gateway shells out to CodexBar
// (`brew install --cask codexbar`), which already speaks to both dashboards.
// A fetch takes several seconds, so the result is polled in the background and
// served from cache; Android can force a fresh read.
package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

const (
	// DefaultCommand is the CodexBar CLI, linked into PATH by its cask.
	DefaultCommand = "codexbar"
	// Disabled turns usage reporting off from the config file.
	Disabled = "off"

	DefaultInterval = 5 * time.Minute
	fetchTimeout    = 60 * time.Second
)

// DefaultArgs asks CodexBar for Claude and Codex as JSON on stdout.
var DefaultArgs = []string{"usage", "--provider", "both", "--format", "json"}

type Service struct {
	cmd      string
	args     []string
	interval time.Duration
	timeout  time.Duration

	mu       sync.Mutex
	snap     model.Usage
	inflight chan struct{} // non-nil while a fetch runs; closed when it ends
}

// New returns nil when usage reporting is turned off.
func New(command string, interval time.Duration) *Service {
	if command == "" {
		command = DefaultCommand
	}
	if command == Disabled || command == "none" {
		return nil
	}
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Service{cmd: command, args: DefaultArgs, interval: interval, timeout: fetchTimeout}
}

// Snapshot returns the cached reading without touching the network.
func (s *Service) Snapshot() model.Usage {
	if s == nil {
		return model.Usage{Providers: []model.UsageProvider{}, Error: "usage reporting is disabled"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.snap
	out.Providers = append([]model.UsageProvider{}, s.snap.Providers...)
	return out
}

// Refresh reads the limits again. Callers that arrive while a read is running
// wait for it instead of starting a second one. A failed read keeps the
// previous providers and reports the error alongside them.
func (s *Service) Refresh(ctx context.Context) model.Usage {
	if s == nil {
		return s.Snapshot()
	}
	s.mu.Lock()
	if done := s.inflight; done != nil {
		s.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
		}
		return s.Snapshot()
	}
	done := make(chan struct{})
	s.inflight = done
	s.mu.Unlock()

	// Keep fetching even if the requesting client hangs up: the result is
	// cached for everyone.
	providers, err := s.fetch(context.WithoutCancel(ctx))

	s.mu.Lock()
	if err != nil {
		s.snap.Error = err.Error()
	} else {
		now := time.Now().UTC()
		s.snap = model.Usage{Providers: providers, FetchedAt: &now}
	}
	s.inflight = nil
	s.mu.Unlock()
	close(done)

	if err != nil {
		slog.Warn("usage refresh failed", "operation", "usage", "command", s.cmd, "error", err)
	}
	return s.Snapshot()
}

// Run refreshes in the background until ctx is done.
func (s *Service) Run(ctx context.Context) {
	if s == nil {
		return
	}
	for {
		s.Refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.interval):
		}
	}
}

func (s *Service) fetch(ctx context.Context) ([]model.UsageProvider, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.cmd, s.args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("%s not found; install it with `brew install --cask codexbar`", s.cmd)
		}
		if msg := firstLine(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s: %w: %s", s.cmd, err, msg)
		}
		return nil, fmt.Errorf("%s: %w", s.cmd, err)
	}
	return parse(stdout.Bytes())
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// --- CodexBar JSON ---

type cbEntry struct {
	Provider string   `json:"provider"`
	Error    string   `json:"error"`
	Usage    *cbUsage `json:"usage"`
}

type cbUsage struct {
	AccountEmail string `json:"accountEmail"`
	LoginMethod  string `json:"loginMethod"`
	Identity     struct {
		AccountEmail string `json:"accountEmail"`
		LoginMethod  string `json:"loginMethod"`
	} `json:"identity"`
	Primary          *cbWindow `json:"primary"`
	Secondary        *cbWindow `json:"secondary"`
	Tertiary         *cbWindow `json:"tertiary"`
	ExtraRateWindows []struct {
		ID     string    `json:"id"`
		Title  string    `json:"title"`
		Window *cbWindow `json:"window"`
	} `json:"extraRateWindows"`
	UpdatedAt string `json:"updatedAt"`
}

type cbWindow struct {
	UsedPercent   float64 `json:"usedPercent"`
	WindowMinutes int     `json:"windowMinutes"`
	ResetsAt      string  `json:"resetsAt"`
}

var displayNames = map[string]string{"claude": "Claude", "codex": "Codex"}

func parse(b []byte) ([]model.UsageProvider, error) {
	var entries []cbEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, fmt.Errorf("parse usage output: %w", err)
	}
	out := make([]model.UsageProvider, 0, len(entries))
	for _, e := range entries {
		if e.Provider == "" {
			continue
		}
		p := model.UsageProvider{
			Provider:    e.Provider,
			DisplayName: displayName(e.Provider),
			Windows:     []model.UsageWindow{},
			Error:       e.Error,
		}
		if e.Usage == nil {
			if p.Error == "" {
				p.Error = "no usage data"
			}
			out = append(out, p)
			continue
		}
		u := e.Usage
		p.Plan = firstNonEmpty(u.LoginMethod, u.Identity.LoginMethod)
		p.Account = firstNonEmpty(u.AccountEmail, u.Identity.AccountEmail)
		p.UpdatedAt = parseTime(u.UpdatedAt)
		for _, named := range []struct {
			key string
			w   *cbWindow
		}{{"primary", u.Primary}, {"secondary", u.Secondary}, {"tertiary", u.Tertiary}} {
			if win, ok := convert(named.key, "", named.w); ok {
				p.Windows = append(p.Windows, win)
			}
		}
		for _, x := range u.ExtraRateWindows {
			if win, ok := convert(x.ID, x.Title, x.Window); ok {
				p.Windows = append(p.Windows, win)
			}
		}
		if len(p.Windows) == 0 && p.Error == "" {
			p.Error = "no limits reported"
		}
		out = append(out, p)
	}
	return out, nil
}

func convert(key, scope string, w *cbWindow) (model.UsageWindow, bool) {
	if w == nil {
		return model.UsageWindow{}, false
	}
	label := windowLabel(w.WindowMinutes)
	if label == "" {
		label = key
	}
	return model.UsageWindow{
		Key:           key,
		Label:         label,
		Scope:         scope,
		UsedPercent:   clampPercent(w.UsedPercent),
		WindowMinutes: w.WindowMinutes,
		ResetsAt:      parseTime(w.ResetsAt),
	}, true
}

// windowLabel turns a window length into "5h" / "7d".
func windowLabel(minutes int) string {
	switch {
	case minutes <= 0:
		return ""
	case minutes%(60*24) == 0:
		return fmt.Sprintf("%dd", minutes/(60*24))
	case minutes%60 == 0:
		return fmt.Sprintf("%dh", minutes/60)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

func clampPercent(v float64) int {
	n := int(math.Round(v))
	if n < 0 {
		return 0
	}
	if n > 100 {
		return 100
	}
	return n
}

func parseTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	t = t.UTC()
	return &t
}

func displayName(provider string) string {
	if n, ok := displayNames[provider]; ok {
		return n
	}
	return provider
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
