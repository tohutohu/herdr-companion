package dsh

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
	"github.com/tohutohu/herdr-android-client/gateway/internal/resident"
)

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
type event struct {
	Type string          `json:"type"`
	Seq  int64           `json:"seq"`
	Time int64           `json:"time"`
	Data json.RawMessage `json:"data"`
}
type eventData struct {
	Error struct {
		Code   string `json:"code"`
		Reason string `json:"reason"`
	} `json:"error"`
	Usage struct {
		InputTokens      int64 `json:"inputTokens"`
		OutputTokens     int64 `json:"outputTokens"`
		CacheReadTokens  int64 `json:"cacheReadTokens"`
		CacheWriteTokens int64 `json:"cacheWriteTokens"`
	} `json:"usage"`
	Source struct {
		Kind string `json:"kind"`
	} `json:"source"`
	Content []contentBlock `json:"content"`
	Message struct {
		Content    []contentBlock `json:"content"`
		ToolCallID string         `json:"toolCallId"`
	} `json:"message"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	CallID    string `json:"callId"`
	Reason    struct {
		Kind  string `json:"kind"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"reason"`
	Preset string `json:"preset"`
}
type wireQuestion struct {
	ID          string         `json:"id"`
	Question    string         `json:"question"`
	Header      string         `json:"header"`
	Detail      string         `json:"detail"`
	Options     []model.Option `json:"options"`
	MultiSelect bool           `json:"multiSelect"`
	Intent      struct {
		Kind string `json:"kind"`
	} `json:"intent"`
}
type pendingRequest struct {
	WireID      json.RawMessage
	RequestID   string
	CallID      string
	Interaction model.Interaction
	Message     model.Message
}

func (p *Provider) handle(s *session, c *conn, f frame) {
	if len(f.ID) > 0 {
		p.request(s, c, f)
		return
	}
	var n struct {
		SessionID string `json:"sessionId"`
		Status    string `json:"status"`
		Event     event  `json:"event"`
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(f.Params, &n); err != nil {
		p.recordError(s.Summary.NativeID, err)
		return
	}
	p.mu.Lock()
	if s.conn != c {
		p.mu.Unlock()
		return
	}
	var err error
	if f.Method == "user-question.cancelled" {
		for key, r := range s.pending {
			if r.RequestID == n.RequestID {
				delete(s.pending, key)
			}
		}
		s.info.Waiting = len(s.pending) > 0
	} else if n.SessionID != s.Summary.NativeID {
		p.mu.Unlock()
		return // this process also announces its subagents
	} else {
		switch f.Method {
		case "session.status":
			s.activity++
			if n.Status == "running" {
				s.info.Running = true
			} else if n.Status == "idle" {
				s.info.Running = false
			}
		case "session.event":
			err = p.eventLocked(s, n.Event)
		}
	}
	if err != nil {
		s.info.Error = err.Error()
		s.info.LastTurnFailed = true
	}
	p.mu.Unlock()
	if err != nil {
		p.recordError(s.Summary.NativeID, err)
		go c.close()
	}
	p.notify(s.Summary.NativeID)
}

func (p *Provider) eventLocked(s *session, e event) error {
	switch e.Type {
	case "user/message", "assistant/message", "tool/call", "tool/result", "turn/start", "turn/end", "permission/preset":
	default:
		return nil // plugin events have independent payload schemas
	}
	var d eventData
	if err := json.Unmarshal(e.Data, &d); err != nil {
		return fmt.Errorf("decode DSH %s: %w", e.Type, err)
	}
	ts := time.UnixMilli(e.Time)
	if e.Time == 0 {
		ts = time.Now()
	}
	m := model.Message{ID: fmt.Sprintf("event-%d", e.Seq), Timestamp: ts}
	switch e.Type {
	case "user/message":
		if d.Source.Kind != "" && d.Source.Kind != "user" && d.Source.Kind != "user-question-reply" {
			return nil
		}
		m.Role = model.RoleUser
		m.Blocks = displayBlocks(d.Content)
	case "assistant/message":
		m.Role = model.RoleAssistant
		m.Blocks = displayBlocks(d.Message.Content)
		if p.unattended != nil {
			p.unattended.text = messageText(m)
			// The journal becomes a session's transcript when the run is
			// promoted: keep what the answer says, not its contract JSON.
			if shown, ok := resident.DisplayText([]byte(p.unattended.text)); ok {
				m.Blocks = []model.Block{model.TextBlock(shown)}
			}
			p.unattended.usage.InputTokens += d.Usage.InputTokens + d.Usage.CacheReadTokens + d.Usage.CacheWriteTokens
			p.unattended.usage.CachedInputTokens += d.Usage.CacheReadTokens
			p.unattended.usage.OutputTokens += d.Usage.OutputTokens
		}
	case "tool/call":
		if p.unattended != nil {
			p.unattended.text = ""
		}
		m.Role = model.RoleTool
		m.Blocks = []model.Block{model.TextBlock(d.Name + "\n" + d.Arguments)}
	case "tool/result":
		m.Role = model.RoleTool
		m.Blocks = displayBlocks(d.Message.Content)
		if p.unattended != nil && d.Error.Code == "FS_SANDBOX_DENIED" {
			p.unattended.denials = append(p.unattended.denials, d.Error.Code+": "+messageText(m))
		}
		for key, r := range s.pending {
			if r.CallID != "" && r.CallID == d.Message.ToolCallID {
				delete(s.pending, key)
			}
		}
		s.info.Waiting = len(s.pending) > 0
	case "turn/start":
		if p.unattended != nil {
			p.unattended.text = ""
		}
		s.activity++
		s.info.Running = true
		s.Summary.LastTurnFailed = false
	case "turn/end":
		s.Turns++
		s.info.Turns = s.Turns
		s.info.LastTurnFailed = d.Reason.Kind == "error" || d.Reason.Kind == "interrupted" || d.Reason.Kind == "blocked"
		if p.unattended != nil && d.Reason.Kind != "completed" {
			s.info.LastTurnFailed = true
		}
		s.Summary.LastTurnFailed = s.info.LastTurnFailed
		s.info.Error = d.Reason.Error.Message
		s.Summary.CompletionRevision = fmt.Sprintf("%d", e.Seq)
		s.pending = map[string]*pendingRequest{}
		s.info.Waiting = false
		if s.info.LastTurnFailed {
			text := d.Reason.Error.Message
			if text == "" {
				text = "DSH turn ended: " + d.Reason.Kind
			}
			m.Role = model.RoleSystem
			m.Blocks = []model.Block{model.TextBlock(text)}
		}
	case "permission/preset":
		s.Summary.Mode = d.Preset
		s.info.Mode = d.Preset
		s.info.ModeID = d.Preset
	default:
		return nil // model context and plugin metadata are not UI messages
	}
	s.Summary.UpdatedAt = ts
	if len(m.Blocks) > 0 {
		if err := p.appendLocked(s, m); err != nil {
			return err
		}
		if m.Role == model.RoleUser || m.Role == model.RoleAssistant {
			text := messageText(m)
			if text != "" {
				s.Summary.LastMessage = providers.OneLine(text, 240)
			}
			if s.Summary.Title == "" && m.Role == model.RoleUser {
				s.Summary.Title = providers.OneLine(text, 80)
			}
		}
	}
	return p.saveLocked(s)
}

func displayBlocks(content []contentBlock) []model.Block {
	var out []model.Block
	for _, b := range content {
		if b.Type == "text" && b.Text != "" {
			out = append(out, model.TextBlock(b.Text))
		}
		if b.Type == "image" {
			out = append(out, model.TextBlock("[Image]"))
		}
	}
	return out
}
func messageText(m model.Message) string {
	var texts []string
	for _, b := range m.Blocks {
		if b.Type == model.BlockText {
			texts = append(texts, b.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func (p *Provider) appendLocked(s *session, m model.Message) error {
	f, err := os.OpenFile(filepath.Join(p.dir, s.Summary.NativeID+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = json.NewEncoder(f).Encode(m); err != nil {
		return err
	}
	return f.Sync()
}

func (p *Provider) Messages(ctx context.Context, id string, _ *providers.Live) ([]model.Message, error) {
	p.mu.Lock()
	s := p.sessions[id]
	if s == nil {
		p.mu.Unlock()
		return nil, providers.ErrNotFound
	}
	pending := make(map[string]bool, len(s.pending))
	for key := range s.pending {
		pending[key] = true
	}
	out := []model.Message{}
	f, err := os.Open(filepath.Join(p.dir, id+".jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		p.mu.Unlock()
		return out, nil
	}
	if err != nil {
		p.mu.Unlock()
		return nil, err
	}
	defer f.Close()
	stat, err := f.Stat()
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	// Snapshot a complete append boundary, then release the provider lock so
	// reading a long transcript does not delay other sessions or approvals.
	scan := bufio.NewScanner(io.LimitReader(f, stat.Size()))
	scan.Buffer(make([]byte, 64<<10), maxFrame)
	positions := map[string]int{}
	for scan.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var m model.Message
		if err := json.Unmarshal(scan.Bytes(), &m); err != nil {
			return nil, err
		}
		for i := range m.Blocks {
			if m.Role == model.RoleAssistant && m.Blocks[i].Type == model.BlockText {
				m.Blocks[i].Report = true
			}
			ia := m.Blocks[i].Interaction
			if ia != nil && ia.State == model.InteractionPending {
				if !pending[ia.ID] {
					ia.State = model.InteractionClosed
				}
			}
		}
		if i, ok := positions[m.ID]; ok {
			out[i] = m
		} else {
			positions[m.ID] = len(out)
			out = append(out, m)
		}
	}
	return out, scan.Err()
}

func (p *Provider) request(s *session, c *conn, f frame) {
	if p.unattended != nil {
		p.residentRequest(c, f)
		return
	}
	var r struct {
		SessionID string         `json:"sessionId"`
		RequestID string         `json:"requestId"`
		ToolName  string         `json:"toolName"`
		Reason    string         `json:"reason"`
		CallID    string         `json:"callId"`
		Questions []wireQuestion `json:"questions"`
	}
	if err := json.Unmarshal(f.Params, &r); err != nil {
		c.write(map[string]any{"jsonrpc": "2.0", "id": f.ID, "error": rpcError{Code: -32602, Message: "invalid request"}})
		return
	}
	// Include a per-process nonce: old UI answers must never approve a new
	// operation whose JSON-RPC integer happens to be reused after resume.
	id := c.tag + ":" + string(f.ID)
	ia := model.Interaction{ID: id, State: model.InteractionPending, Supported: true}
	switch f.Method {
	case "approval/request":
		ia.Type = model.InteractionApproval
		ia.Title = r.ToolName
		ia.Detail = r.Reason
		ia.Decisions = []string{model.DecisionApprove, model.DecisionDeny}
	case "user-question/ask":
		ia.Type = model.InteractionQuestions
		for _, w := range r.Questions {
			q := model.Question{ID: w.ID, Question: w.Question, Header: w.Header, Options: w.Options, AllowOther: w.Intent.Kind != "approval", Type: model.QuestionSelect}
			if w.MultiSelect {
				q.Type = model.QuestionMultiSelect
			}
			if len(w.Options) == 0 {
				q.Type = model.QuestionText
			}
			if w.Detail != "" {
				ia.Detail += w.Detail + "\n"
			}
			if w.Intent.Kind == "plan-review" || w.Intent.Kind == "approval" {
				ia.Kind = model.KindPlan
			}
			ia.Questions = append(ia.Questions, q)
		}
		if len(ia.Questions) == 0 {
			c.reply(f.ID, map[string]bool{"cancelled": true})
			return
		}
	default:
		c.write(map[string]any{"jsonrpc": "2.0", "id": f.ID, "error": rpcError{Code: -32601, Message: "unsupported client request"}})
		return
	}
	m := model.Message{ID: "interaction-" + id, Role: model.RoleAssistant, Timestamp: time.Now(), Blocks: []model.Block{{Type: model.BlockInteraction, Interaction: &ia}}}
	p.mu.Lock()
	if s.conn != c || s.stopping {
		p.mu.Unlock()
		c.reply(f.ID, map[string]string{"outcome": "cancelled"})
		return
	}
	err := p.appendLocked(s, m)
	if err == nil {
		s.pending[id] = &pendingRequest{WireID: f.ID, RequestID: r.RequestID, CallID: r.CallID, Interaction: ia, Message: m}
		s.info.Waiting = true
	}
	p.mu.Unlock()
	if err != nil {
		p.recordError(s.Summary.NativeID, err)
		go c.close()
	}
	p.notify(s.Summary.NativeID)
}

func (p *Provider) Respond(_ context.Context, id string, _ *providers.Live, response model.InteractionResponse) error {
	s, err := p.get(id)
	if err != nil {
		return err
	}
	// Do not take s.op: a command can be waiting on this very response.
	p.mu.Lock()
	r := s.pending[response.InteractionID]
	c := s.conn
	if r == nil || c == nil {
		p.mu.Unlock()
		return providers.ErrInteractionGone
	}
	var result any
	if r.Interaction.Type == model.InteractionApproval {
		outcome := "rejected"
		switch response.Decision {
		case model.DecisionApprove:
			outcome = "allowed-once"
		case model.DecisionDeny:
		default:
			p.mu.Unlock()
			return errors.New("DSH only supports one-time approve or deny")
		}
		result = map[string]string{"outcome": outcome}
	} else {
		answers := []map[string]any{}
		if len(response.Answers) != len(r.Interaction.Questions) {
			p.mu.Unlock()
			return errors.New("answer every question exactly once")
		}
		for _, q := range r.Interaction.Questions {
			a, ok := response.Answers[q.ID]
			if !ok || len(a.Selected) == 0 && strings.TrimSpace(a.Text) == "" {
				p.mu.Unlock()
				return errors.New("missing question answer")
			}
			if !q.AllowOther && a.Text != "" || q.Type != model.QuestionMultiSelect && len(a.Selected) > 1 {
				p.mu.Unlock()
				return errors.New("invalid question answer")
			}
			seen := map[string]bool{}
			for _, value := range a.Selected {
				valid := false
				for _, option := range q.Options {
					if option.Label == value {
						valid = true
					}
				}
				if !valid || seen[value] {
					p.mu.Unlock()
					return errors.New("invalid selected option")
				}
				seen[value] = true
			}
			selected := a.Selected
			if selected == nil {
				selected = []string{}
			}
			answer := map[string]any{"id": q.ID, "selected": selected}
			if a.Text != "" {
				answer["custom"] = a.Text
			}
			answers = append(answers, answer)
		}
		result = map[string]any{"answers": answers}
	}
	// Claim before writing so concurrent retries cannot grant twice.
	delete(s.pending, response.InteractionID)
	s.info.Waiting = len(s.pending) > 0
	r.Interaction.State = model.InteractionAnswered
	b, _ := json.Marshal(result)
	r.Interaction.Answer = string(b)
	r.Message.Blocks[0].Interaction = &r.Interaction
	err = p.appendLocked(s, r.Message)
	p.mu.Unlock()
	if err == nil {
		err = c.reply(r.WireID, result)
	}
	if err != nil {
		c.close()
	}
	p.notify(id)
	return err
}

func inputBlocks(in model.Input) ([]any, error) {
	var out []any
	if text := providers.TextWithFiles(in); text != "" {
		out = append(out, map[string]string{"type": "text", "text": text})
	}
	// SDK images are inline base64. Bound aggregate memory independently of
	// the 500 MiB streamed file-upload limit; larger files remain path inputs.
	remaining := 20 << 20
	for _, path := range in.Images {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(f, int64(remaining)+1))
		f.Close()
		if err != nil {
			return nil, err
		}
		if len(b) > remaining {
			return nil, errors.New("DSH inline images exceed 20 MiB; attach as files instead")
		}
		remaining -= len(b)
		mime := http.DetectContentType(b)
		if mime != "image/png" && mime != "image/jpeg" && mime != "image/webp" && mime != "image/gif" {
			return nil, errors.New("unsupported DSH image format")
		}
		out = append(out, map[string]string{"type": "image", "mimeType": mime, "data": base64.StdEncoding.EncodeToString(b)})
	}
	if len(out) == 0 {
		return nil, errors.New("empty DSH prompt")
	}
	return out, nil
}
