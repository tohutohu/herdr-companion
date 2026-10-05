package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

const maxSummaryRecord = 1 << 20

type summaryRolloutEntry struct {
	mu                 sync.Mutex
	info               os.FileInfo
	offset             int64
	used               time.Time
	state              rolloutSummary
	completionKey      string
	completionVersion  displayVersion
	completionRevision string
	resetKnown         bool
	resetVersion       displayVersion
}

// Only projections needed by lists and notifications survive the scan. Tool
// output, images, reasoning and historical assistant text are never retained.
type rolloutSummary struct {
	available                bool
	lastText                 string
	turnID, status, planID   string
	model, effort, cwd, mode string
	tokens                   *tokenInfo
	async                    []json.RawMessage
	asyncBytes               int
	unsupported              bool
	contentVersion           uint64
	turnAt                   time.Time
}

func (p *Provider) summaryVersion(id string) displayVersion {
	v := p.displayVersion(id, false)
	v.events = 0
	if d := p.currentDaemon(); d != nil {
		d.mu.Lock()
		v.events = d.summaryVersions[id]
		d.mu.Unlock()
	}
	return v
}

func (p *Provider) checkSummaryVersion(id, path string, state rolloutSummary) rolloutSummary {
	v := p.summaryVersion(id)
	p.displayMu.Lock()
	e := p.summaryEntries[path]
	p.displayMu.Unlock()
	if e == nil {
		return state
	}
	e.mu.Lock()
	if e.resetKnown && e.resetVersion != v {
		e.state.unsupported, state.unsupported = true, true
	}
	e.resetKnown, e.resetVersion = true, v
	e.mu.Unlock()
	return state
}

func (p *Provider) rolloutSummary(ctx context.Context, path string) (rolloutSummary, error) {
	p.displayMu.Lock()
	if p.summaryEntries == nil {
		p.summaryEntries = map[string]*summaryRolloutEntry{}
	}
	e := p.summaryEntries[path]
	if e == nil {
		if len(p.summaryEntries) >= maxDisplayEntries {
			oldest := ""
			for key, entry := range p.summaryEntries {
				if oldest == "" || entry.used.Before(p.summaryEntries[oldest].used) {
					oldest = key
				}
			}
			delete(p.summaryEntries, oldest)
		}
		e = &summaryRolloutEntry{}
		p.summaryEntries[path] = e
	}
	e.used = time.Now()
	p.displayMu.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	f, err := os.Open(path)
	if err != nil {
		return rolloutSummary{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return rolloutSummary{}, err
	}
	if sameRollout(e.info, st) {
		return e.state, nil
	}
	if e.info == nil || !os.SameFile(e.info, st) || st.Size() < e.info.Size() || (st.Size() == e.info.Size() && !st.ModTime().Equal(e.info.ModTime())) {
		// Preserve monotonic revisions: a rewritten file can contain the same
		// number of records describing different results within the same turn.
		version := e.state.contentVersion + 1
		e.offset, e.state = 0, rolloutSummary{contentVersion: version}
	}
	r := bufio.NewReaderSize(io.NewSectionReader(f, e.offset, st.Size()-e.offset), 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return rolloutSummary{}, err
		}
		line, n, complete, err := summaryRecord(r)
		if complete {
			e.state.consume(line)
			e.offset += n
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return rolloutSummary{}, err
			}
			break
		}
	}
	e.info = st
	return e.state, nil
}

// Keep at most a bounded prefix of oversized records; still advance across
// their entire line. Incomplete records are retried from their original offset.
func summaryRecord(r *bufio.Reader) ([]byte, int64, bool, error) {
	var kept []byte
	var n int64
	for {
		part, err := r.ReadSlice('\n')
		n += int64(len(part))
		if len(kept)+len(part) <= maxSummaryRecord {
			kept = append(kept, part...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return kept, n, len(part) > 0 && part[len(part)-1] == '\n', err
	}
}

type summaryRecordHead struct{ record, payload, item string }

// Read only the actual envelope tags, stopping before large text/output
// fields. Tool output quoting "task_complete" must not become a lifecycle event.
func summaryRecordHeader(line []byte) (head summaryRecordHead) {
	d := json.NewDecoder(bytes.NewReader(line))
	if tok, err := d.Token(); err != nil || tok != json.Delim('{') {
		return
	}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return
		}
		switch key {
		case "type":
			if d.Decode(&head.record) != nil {
				return
			}
			if head.record == "compacted" || head.record == "turn_context" {
				return
			}
		case "payload":
			if tok, err := d.Token(); err != nil || tok != json.Delim('{') {
				return
			}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return
				}
				if key == "type" {
					if d.Decode(&head.payload) != nil {
						return
					}
					if head.payload != "item_completed" {
						return
					}
				} else if key == "item" {
					if tok, err := d.Token(); err != nil || tok != json.Delim('{') {
						return
					}
					for d.More() {
						key, err := d.Token()
						if err != nil {
							return
						}
						if key == "type" {
							_ = d.Decode(&head.item)
							return
						}
						var skip json.RawMessage
						if d.Decode(&skip) != nil {
							return
						}
					}
				} else {
					var skip json.RawMessage
					if d.Decode(&skip) != nil {
						return
					}
				}
			}
			return
		default:
			var skip json.RawMessage
			if d.Decode(&skip) != nil {
				return
			}
		}
	}
	return
}

func (s *rolloutSummary) consume(line []byte) {
	head := summaryRecordHeader(line)
	if head.record == "response_item" || head.record == "compacted" {
		s.contentVersion++
	}
	switch head.payload {
	case "item_completed", "task_complete", "turn_aborted", "agent_message", "user_message", "error":
		s.contentVersion++
	}
	if head.record == "response_item" && head.payload != "message" {
		if bytes.Contains(line, []byte(`"request_user_input_async"`)) {
			s.unsupported = true
		}
		return
	}
	if head.payload == "item_completed" {
		switch head.item {
		case "CommandExecution", "commandExecution", "Reasoning", "reasoning", "FileChange", "fileChange", "SubAgentActivity", "subAgentActivity", "ImageView", "imageView", "ContextCompaction", "contextCompaction":
			return
		}
	}
	var rec struct {
		Type      string          `json:"type"`
		Timestamp time.Time       `json:"timestamp"`
		Payload   json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(line, &rec) != nil {
		// An oversized/malformed relevant record needs canonical reconciliation;
		// known tool-only records were already skipped using their decoded tags.
		s.unsupported = true
		return
	}
	if rec.Type == "response_item" && head.record != "response_item" {
		s.contentVersion++
	}
	var q struct {
		Type              string             `json:"type"`
		TurnID            string             `json:"turn_id"`
		Model             string             `json:"model"`
		Effort            string             `json:"effort"`
		Cwd               string             `json:"cwd"`
		Role              string             `json:"role"`
		Content           []summaryContent   `json:"content"`
		Message           string             `json:"message"`
		LastMessage       *string            `json:"last_agent_message"`
		Item              json.RawMessage    `json:"item"`
		Info              *tokenInfo         `json:"info"`
		CollaborationMode *collaborationMode `json:"collaboration_mode"`
		ThreadSettings    struct {
			CollaborationMode *collaborationMode `json:"collaboration_mode"`
		} `json:"thread_settings"`
	}
	if json.Unmarshal(rec.Payload, &q) != nil {
		return
	}
	switch rec.Type {
	case "compacted":
		// Compaction/reversion semantics belong to app-server. Reconcile via RPC.
		s.unsupported = true
	case "turn_context":
		s.available = true
		if q.TurnID != s.turnID {
			s.turnID, s.status, s.planID = q.TurnID, "inProgress", ""
		}
		s.turnAt = rec.Timestamp
		s.model, s.effort, s.cwd = q.Model, q.Effort, q.Cwd
		if q.CollaborationMode != nil {
			s.mode = q.CollaborationMode.Mode
		}
	case "response_item":
		if q.Type == "message" {
			s.text(q.Role, q.Content)
		}
	case "event_msg":
		switch q.Type {
		case "task_started":
			s.available = true
			s.turnID, s.status, s.planID = q.TurnID, "inProgress", ""
			s.turnAt = rec.Timestamp
		case "task_complete":
			s.available = true
			s.turnID, s.status = q.TurnID, "completed"
			s.turnAt = rec.Timestamp
			if q.LastMessage != nil && strings.TrimSpace(*q.LastMessage) != "" {
				s.lastText = providers.OneLine(*q.LastMessage, 160)
			}
		case "turn_aborted":
			s.turnID, s.status = q.TurnID, "interrupted"
			s.turnAt = rec.Timestamp
		case "error":
			s.unsupported = true // distinguish retryable errors using canonical history
		case "token_count":
			if q.Info != nil {
				copy := *q.Info
				if s.tokens != nil {
					if copy.context() == nil {
						copy.LastTokenUsage = s.tokens.LastTokenUsage
						copy.ModelContextWindow = s.tokens.ModelContextWindow
					}
					if copy.TotalTokenUsage.InputTokens == 0 {
						copy.TotalTokenUsage = s.tokens.TotalTokenUsage
					}
				}
				s.tokens = &copy
			}
		case "agent_message":
			if q.Message != "" {
				s.text("assistant", []summaryContent{{Text: q.Message}})
			}
		case "user_message":
			if q.Message != "" {
				s.text("user", []summaryContent{{Text: q.Message}})
			}
		case "thread_settings_applied":
			if q.ThreadSettings.CollaborationMode != nil {
				s.mode = q.ThreadSettings.CollaborationMode.Mode
			}
		case "item_completed":
			s.item(q.Item)
		}
	}
}

type summaryContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (s *rolloutSummary) text(role string, content []summaryContent) {
	if role != "assistant" && role != "user" {
		return
	}
	for _, c := range content {
		if c.Text == "" {
			continue
		}
		if role == "user" && len(parseAsyncReplies(c.Text)) > 0 {
			raw, _ := json.Marshal(asyncHistoryItem{Type: "userMessage", Content: []userInput{{Type: "text", Text: c.Text}}})
			s.keepAsync(raw)
			continue
		}
		if strings.TrimSpace(c.Text) != "" {
			s.available = true
			s.lastText = providers.OneLine(c.Text, 160)
			return
		}
	}
}

func (s *rolloutSummary) item(raw json.RawMessage) {
	var it struct {
		Type, ID, Text, Delivery string
		Content                  []summaryContent
		Questions                []asyncQuestion
		Kind                     string
	}
	if json.Unmarshal(raw, &it) != nil {
		return
	}
	switch it.Type {
	case "AgentMessage", "agentMessage":
		if it.Delivery == "async" {
			projection, _ := json.Marshal(asyncHistoryItem{Type: "agentMessage", ID: it.ID, Delivery: it.Delivery, Questions: it.Questions})
			s.keepAsync(projection)
		}
		if it.Text != "" {
			s.text("assistant", []summaryContent{{Text: it.Text}})
		} else {
			s.text("assistant", it.Content)
		}
	case "UserMessage", "userMessage":
		s.text("user", it.Content)
	case "Plan", "plan":
		s.planID = it.ID
	case "Extension":
		if strings.Contains(it.Kind, "question") || strings.Contains(it.Kind, "input") {
			s.unsupported = true
		}
	}
}

func (s *rolloutSummary) keepAsync(raw json.RawMessage) {
	if s.asyncBytes+len(raw) > maxSummaryRecord {
		s.unsupported = true
		return
	}
	s.async = append(s.async, raw)
	s.asyncBytes += len(raw)
}

func (s rolloutSummary) thread(meta *Thread) *Thread {
	th := *meta
	if s.model != "" {
		th.Model = s.model
	}
	if s.effort != "" {
		effort := s.effort
		th.Effort = &effort
	}
	if th.Cwd == "" && s.cwd != "" {
		th.Cwd = s.cwd
	}
	turn := Turn{ID: s.turnID, Status: s.status, Items: append([]json.RawMessage(nil), s.async...)}
	if s.planID != "" {
		raw, _ := json.Marshal(itemHead{Type: "plan", ID: s.planID})
		turn.Items = append(turn.Items, raw)
	}
	th.Turns = []Turn{turn}
	return &th
}

// Finished turns may acquire background results without a new turn ID. Keep
// the canonical revision used by notification deduplication, but compute it
// only when content changes, not on token accounting or terminal redraws.
func (p *Provider) rolloutCompletion(ctx context.Context, meta *Thread, state rolloutSummary, turn *Turn) (string, error) {
	key := fmt.Sprintf("%s:%s:%d", turn.ID, turn.Status, state.contentVersion)
	v := p.displayVersion(meta.ID, true)
	p.displayMu.Lock()
	e := p.summaryEntries[meta.Path]
	p.displayMu.Unlock()
	if e != nil {
		e.mu.Lock()
		if e.completionKey == key && e.completionVersion == v {
			rev := e.completionRevision
			e.mu.Unlock()
			return rev, nil
		}
		e.mu.Unlock()
	}
	th, err := p.displayThread(ctx, meta.ID)
	if err != nil {
		return "", err
	}
	lt := lastTurn(th)
	if lt == nil || lt.ID != turn.ID || lt.Status != turn.Status {
		return "", nil
	}
	rev := completionRevision(lt)
	if e != nil && v == p.displayVersion(meta.ID, true) {
		e.mu.Lock()
		if e.state.contentVersion == state.contentVersion && sameRollout(e.info, rolloutStat(meta.Path)) {
			e.completionKey, e.completionVersion, e.completionRevision = key, v, rev
		}
		e.mu.Unlock()
	}
	return rev, nil
}

// An unsupported record (e.g. compaction) is reconciled once from app-server;
// subsequent appends resume the lightweight scan at the recorded offset.
func (p *Provider) seedRolloutSummary(path string, before os.FileInfo, th *Thread) {
	p.displayMu.Lock()
	e := p.summaryEntries[path]
	p.displayMu.Unlock()
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !sameRollout(before, rolloutStat(path)) || !sameRollout(e.info, before) || e.resetVersion != p.summaryVersion(th.ID) {
		return
	}
	s := rolloutSummary{available: true, contentVersion: e.state.contentVersion, lastText: lastThreadText(th), turnAt: e.state.turnAt, tokens: e.state.tokens, mode: e.state.mode, model: e.state.model, effort: e.state.effort, cwd: e.state.cwd}
	lines := rolloutTailLines(path)
	if info := tokenInfoFrom(lines); info != nil {
		s.tokens = info
	}
	if mode := collaborationModeFrom(lines); mode != "" {
		s.mode = mode
	}
	if lt := lastTurn(th); lt != nil {
		s.turnID, s.status, s.planID = lt.ID, lt.Status, latestPlanItem(th)
	}
	for _, turn := range th.Turns {
		for _, raw := range turn.Items {
			var it asyncHistoryItem
			if json.Unmarshal(raw, &it) != nil {
				continue
			}
			if it.Type == "agentMessage" && it.Delivery == "async" {
				it.Content = nil
				projection, _ := json.Marshal(it)
				s.keepAsync(projection)
			} else if it.Type == "userMessage" {
				for _, c := range it.Content {
					if len(parseAsyncReplies(c.Text)) > 0 {
						projection, _ := json.Marshal(asyncHistoryItem{Type: "userMessage", Content: []userInput{c}})
						s.keepAsync(projection)
					}
				}
			}
		}
	}
	e.state = s
	if lt := lastTurn(th); lt != nil {
		e.completionKey = fmt.Sprintf("%s:%s:%d", lt.ID, lt.Status, s.contentVersion)
		e.completionVersion = p.displayVersion(th.ID, true)
		e.completionRevision = completionRevision(lt)
	}
}
