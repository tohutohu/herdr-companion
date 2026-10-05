package codex

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

const (
	metadataLifetime  = 5 * time.Second
	historyLifetime   = 30 * time.Second // reconcile missed notifications or buffered rollouts
	maxDisplayEntries = 64
	maxDisplayBytes   = 64 << 20
)

type displayVersion struct {
	client          *rpcClient
	events, actions uint64
}

type metadataEntry struct {
	thread  *Thread
	version displayVersion
	at      time.Time
}

type historyEntry struct {
	thread   *Thread
	info     os.FileInfo
	version  displayVersion
	at, used time.Time
	bytes    int
	root     string
	messages []model.Message
}

func sameRollout(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func rolloutStat(path string) os.FileInfo {
	st, _ := os.Stat(path)
	return st
}

func (p *Provider) displayVersion(id string, history bool) displayVersion {
	v := displayVersion{actions: p.actionVersion.Load()}
	p.mu.Lock()
	defer p.mu.Unlock()
	if d := p.daemon; d != nil && d.c.alive() {
		v.client = d.c
		d.mu.Lock()
		if history {
			v.events = d.historyVersions[id]
		} else {
			v.events = d.metadataVersions[id]
		}
		d.mu.Unlock()
	} else if p.reader != nil && p.reader.alive() {
		v.client = p.reader
	}
	return v
}

// Metadata never retains turns, even if a test/older server returns them.
func (p *Provider) metadata(ctx context.Context, id string) (*Thread, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v := p.displayVersion(id, false)
	p.displayMu.Lock()
	e := p.metadataEntries[id]
	if e != nil && e.version == v && time.Since(e.at) < metadataLifetime {
		th := e.thread
		p.displayMu.Unlock()
		return th, nil
	}
	p.displayMu.Unlock()
	th, err := p.readThread(ctx, id, false)
	if err != nil {
		return nil, err
	}
	copy := *th
	copy.Turns = nil
	after := p.displayVersion(id, false)
	if v == after || (v.client == nil && v.events == after.events && v.actions == after.actions) {
		p.displayMu.Lock()
		if p.metadataEntries == nil {
			p.metadataEntries = map[string]*metadataEntry{}
		}
		if len(p.metadataEntries) >= maxDisplayEntries {
			var oldest string
			for key, entry := range p.metadataEntries {
				if oldest == "" || entry.at.Before(p.metadataEntries[oldest].at) {
					oldest = key
				}
			}
			delete(p.metadataEntries, oldest)
		}
		p.metadataEntries[id] = &metadataEntry{thread: &copy, version: after, at: time.Now()}
		p.displayMu.Unlock()
	}
	return &copy, nil
}

// Action paths still use readThread directly. A display hit requires both a
// stable rollout and unchanged connection/event generation; no file means no hit.
func (p *Provider) displayThread(ctx context.Context, id string) (*Thread, error) {
	meta, err := p.metadata(ctx, id)
	if err != nil {
		return nil, err
	}
	before := rolloutStat(meta.Path)
	v := p.displayVersion(id, true)
	p.displayMu.Lock()
	e := p.historyEntries[id]
	if e != nil && e.version == v && sameRollout(e.info, before) && time.Since(e.at) < historyLifetime {
		e.used = time.Now()
		// Current metadata can change without touching persisted messages.
		copy := *meta
		copy.Turns = e.thread.Turns
		p.displayMu.Unlock()
		return &copy, nil
	}
	p.displayMu.Unlock()
	th, err := p.readThread(ctx, id, true)
	if err != nil {
		return nil, err
	}
	after := rolloutStat(th.Path)
	if v == p.displayVersion(id, true) && meta.Path == th.Path && sameRollout(before, after) {
		n := threadBytes(th)
		p.displayMu.Lock()
		if p.historyEntries == nil {
			p.historyEntries = map[string]*historyEntry{}
		}
		// Remove an old snapshot even when the replacement exceeds the budget.
		delete(p.historyEntries, id)
		if n <= maxDisplayBytes {
			p.historyEntries[id] = &historyEntry{thread: th, info: after, version: v, at: time.Now(), used: time.Now(), bytes: n}
			p.evictHistory()
		}
		p.displayMu.Unlock()
	}
	return th, nil
}

func threadBytes(th *Thread) int {
	n := 1024 + len(th.Source) + len(th.Preview)
	for _, turn := range th.Turns {
		n += 256
		for _, raw := range turn.Items {
			n += len(raw) + 64
		}
	}
	return n
}

// Caller holds displayMu. Entries and message strings are immutable snapshots.
func (p *Provider) evictHistory() {
	for {
		total, oldest := 0, ""
		for id, e := range p.historyEntries {
			total += e.bytes
			if oldest == "" || e.used.Before(p.historyEntries[oldest].used) {
				oldest = id
			}
		}
		if len(p.historyEntries) <= maxDisplayEntries && total <= maxDisplayBytes {
			return
		}
		delete(p.historyEntries, oldest)
	}
}

func (p *Provider) convertedHistory(th *Thread, root string) []model.Message {
	info := rolloutStat(th.Path)
	p.displayMu.Lock()
	e := p.historyEntries[th.ID]
	if e != nil && sameTurns(e.thread, th) && sameRollout(e.info, info) && e.root == root && e.messages != nil {
		msgs := append([]model.Message(nil), e.messages...)
		p.displayMu.Unlock()
		return msgs
	}
	p.displayMu.Unlock()
	msgs := ConvertThread(th, convertOptions{SessionID: gatewayID(th.ID), Root: root, Sink: p.sink, Answered: answeredInputs(th.Path)})
	// Truncated diffs can otherwise retain the entire original string through
	// a short substring. Charge and retain only the displayed projection.
	for i := range msgs {
		for j := range msgs[i].Blocks {
			b := &msgs[i].Blocks[j]
			b.Text = strings.Clone(b.Text)
		}
	}
	p.displayMu.Lock()
	if e != nil && p.historyEntries[th.ID] == e && sameTurns(e.thread, th) && sameRollout(e.info, info) && sameRollout(info, rolloutStat(th.Path)) {
		e.messages, e.root = msgs, root
		e.bytes = threadBytes(e.thread) + messageBytes(msgs)
		p.evictHistory()
	}
	p.displayMu.Unlock()
	return append([]model.Message(nil), msgs...)
}

func sameTurns(a, b *Thread) bool {
	return a == b || (len(a.Turns) > 0 && len(a.Turns) == len(b.Turns) && &a.Turns[0] == &b.Turns[0])
}

func messageBytes(msgs []model.Message) int {
	n := len(msgs) * 128
	for _, m := range msgs {
		for _, b := range m.Blocks {
			n += 128 + len(b.Text) + len(b.URL) + len(b.Path)
			if b.Interaction != nil {
				raw, _ := json.Marshal(b.Interaction)
				n += len(raw) + 1024
			}
		}
	}
	return n
}
