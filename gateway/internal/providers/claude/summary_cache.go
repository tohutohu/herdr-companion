package claude

import (
	"context"
	"os"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// Store only small summaries, never transcripts or image payloads. Live and
// blocked state affect queued prompts and pending approvals even when the file
// has not changed.
const maxCachedSummaries = 96

type summaryKey struct {
	path          string
	live, blocked bool
}
type cachedSummary struct {
	info    os.FileInfo
	summary providers.Summary
	used    time.Time
}

func sameTranscript(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func copySummary(s providers.Summary) providers.Summary {
	if s.Context != nil {
		v := *s.Context
		s.Context = &v
	}
	if s.Cost != nil {
		v := *s.Cost
		s.Cost = &v
	}
	return s
}

func (p *Provider) summaryPath(ctx context.Context, path, id string, live *providers.Live) (providers.Summary, error) {
	if err := ctx.Err(); err != nil {
		return providers.Summary{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return providers.Summary{}, err
	}
	key := summaryKey{path: path, live: live != nil, blocked: live.Blocked()}
	p.summaryMu.Lock()
	cached, ok := p.summaries[key]
	if ok && sameTranscript(cached.info, info) {
		cached.used = time.Now()
		p.summaries[key] = cached
		p.summaryMu.Unlock()
		return copySummary(cached.summary), nil
	}
	p.summaryMu.Unlock()

	// Parsing stays outside the cache lock so unrelated requests and cache hits
	// are not held up by a large transcript.
	opt := ParseOptions{SessionID: gatewayID(id), Live: live}
	var summary providers.Summary
	if live != nil {
		// A running transcript changes between most requests and is the one
		// being read, so decode only what was appended.
		c, err := p.transcript(path, id)
		if err != nil {
			return providers.Summary{}, err
		}
		summary = c.t.summary(opt)
		c.mu.Unlock()
	} else {
		tr, err := p.loadPath(path, id)
		if err != nil {
			return providers.Summary{}, err
		}
		summary = tr.summary(opt)
	}
	summary.NativeID = id
	if err := ctx.Err(); err != nil {
		return providers.Summary{}, err
	}
	after, err := os.Stat(path)
	// A writer may append or replace the transcript during parsing. Return this
	// snapshot, but force the next request to read it again.
	if err == nil && sameTranscript(info, after) {
		p.summaryMu.Lock()
		if p.summaries == nil {
			p.summaries = make(map[summaryKey]cachedSummary)
		}
		if _, exists := p.summaries[key]; !exists && len(p.summaries) >= maxCachedSummaries {
			var oldest summaryKey
			var used time.Time
			for k, v := range p.summaries {
				if used.IsZero() || v.used.Before(used) {
					oldest, used = k, v.used
				}
			}
			delete(p.summaries, oldest)
		}
		p.summaries[key] = cachedSummary{info: after, summary: copySummary(summary), used: time.Now()}
		p.summaryMu.Unlock()
	}
	return summary, nil
}
