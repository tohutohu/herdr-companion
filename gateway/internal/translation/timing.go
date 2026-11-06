package translation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Content and error messages are deliberately excluded: model errors can contain
// user text. Request timings include queueing; inference timings survive a caller
// disconnect and measure the actual work that populated the cache.
type timing struct {
	Time         time.Time `json:"time"`
	Event        string    `json:"event"`
	Model        string    `json:"model"`
	Revision     string    `json:"revision"`
	Target       string    `json:"target"`
	ContextBytes int       `json:"contextBytes,omitempty"`
	InputBytes   int       `json:"inputBytes"`
	OutputBytes  int       `json:"outputBytes"`
	CacheHit     bool      `json:"cacheHit"`
	ElapsedMS    float64   `json:"elapsedMs"`
	QueueMS      float64   `json:"queueMs"`
	StartupMS    float64   `json:"startupMs"`
	InferenceMS  float64   `json:"inferenceMs"`
	Outcome      string    `json:"outcome"`
	Exit         string    `json:"exit,omitempty"`
}

func milliseconds(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func timingOutcome(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "error"
	}
}

func (s *Service) recordTiming(v timing) {
	s.timingMu.Lock()
	defer s.timingMu.Unlock()
	v.Time = time.Now().UTC()
	v.Revision = revisions[v.Model]
	data, err := json.Marshal(v)
	if err != nil || os.MkdirAll(s.dir, 0700) != nil {
		return
	}
	path := filepath.Join(s.dir, "timings.jsonl")
	if stat, err := os.Stat(path); err == nil && stat.Size()+int64(len(data))+1 > 5<<20 {
		if os.Rename(path, filepath.Join(s.dir, "timings.1.jsonl")) != nil {
			return
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}
