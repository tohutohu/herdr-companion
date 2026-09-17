package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// entry is one line of a Claude Code session transcript
// (~/.claude/projects/<project>/<session-id>.jsonl).
type entry struct {
	Type              string          `json:"type"`
	UUID              string          `json:"uuid"`
	Timestamp         time.Time       `json:"timestamp"`
	SessionID         string          `json:"sessionId"`
	Cwd               string          `json:"cwd"`
	IsMeta            bool            `json:"isMeta"`
	IsSidechain       bool            `json:"isSidechain"`
	IsAPIErrorMessage bool            `json:"isApiErrorMessage"`
	Message           *apiMessage     `json:"message"`
	Subtype           string          `json:"subtype"`
	Content           json.RawMessage `json:"content"`   // system and queue-operation entries
	Operation         string          `json:"operation"` // queue-operation entries
	Attachment        *attachment     `json:"attachment"`
	Effort            string          `json:"effort"`         // assistant entries
	PermissionMode    string          `json:"permissionMode"` // user prompts and permission-mode entries
	AITitle           string          `json:"aiTitle"`
	CustomTitle       string          `json:"customTitle"`
}

type apiMessage struct {
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *apiUsage       `json:"usage"`
}

// apiUsage is the token accounting of one assistant reply. Its input side is
// everything the model saw, so it doubles as the size of the context window
// in use at that point.
type apiUsage struct {
	InputTokens         int64 `json:"input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
}

func (u *apiUsage) total() int64 {
	if u == nil {
		return 0
	}
	return u.InputTokens + u.CacheCreationTokens + u.CacheReadTokens + u.OutputTokens
}

// attachment is the payload of an "attachment" entry. Only queued_command
// carries conversation content; "model" attachments name the exact model,
// which assistant entries report without its context-length suffix.
type attachment struct {
	Type     string `json:"type"`
	Prompt   string `json:"prompt"`
	Identity *struct {
		ModelID string `json:"modelId"`
	} `json:"identity"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Source    *imageSource    `json:"source"`
}

type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	URL       string `json:"url"`
}

// Metadata-only entry types that never carry conversation content.
var ignoredTypes = map[string]bool{
	"last-prompt": true, "mode": true, "atis-latch": true,
	"permission-mode": true, "queue-operation": true, "pr-link": true,
	"file-history-snapshot": true, "file-history-delta": true, "cost-state": true,
	"summary": true, "ai-title": true, "custom-title": true, "agent-name": true,
	"tag": true, "progress": true,
}

var ignoredSystemSubtypes = map[string]bool{
	"stop_hook_summary": true, "turn_duration": true, "away_summary": true,
}

const (
	toolAskUserQuestion = "AskUserQuestion"
	maxToolOutput       = 1500
	// attachmentQueuedCommand marks a prompt the user sent while the agent was
	// working, once it is taken into the running turn.
	attachmentQueuedCommand = "queued_command"
	// attachmentModel records the model a session runs on.
	attachmentModel = "model"

	// defaultContextWindow is what every Claude model offers unless its id
	// asks for the long-context variant.
	defaultContextWindow = 200_000
	longContextWindow    = 1_000_000
	longContextSuffix    = "[1m]"
)

// contextWindow is how many tokens the model fits. Claude Code appends
// "[1m]" to the ids of the 1M-token variants.
func contextWindow(modelIDs ...string) int64 {
	for _, id := range modelIDs {
		if strings.Contains(id, longContextSuffix) {
			return longContextWindow
		}
	}
	return defaultContextWindow
}

// ParseOptions carries context needed while converting.
type ParseOptions struct {
	SessionID string // gateway session id ("claude:<id>") for URLs and dead letters
	Root      string // workspace root for file references
	// Live is used to decide whether an unanswered tool call is actionable.
	Live *providers.Live
	Sink deadletter.Sink
}

type toolResult struct {
	block contentBlock
	entry *entry
}

// Transcript holds decoded entries plus derived metadata.
type Transcript struct {
	entries   []*entry
	raws      [][]byte
	toolNames map[string]string // tool_use id -> tool name
	Cwd       string
	Title     string
	Updated   time.Time
	// Model is the latest model: from assistant replies, or a `/model`
	// change made after them.
	Model          string
	Effort         string
	PermissionMode string
	// ModelID is the exact model id, "[1m]" suffix included; only "model"
	// attachments carry it.
	ModelID string
	// ContextTokens is the context the newest main-chain reply consumed.
	ContextTokens int64
}

// Decode reads JSONL. Broken lines are dead-lettered and skipped.
func Decode(r io.Reader, sessionID string, sink deadletter.Sink) (*Transcript, error) {
	t := &Transcript{toolNames: map[string]string{}}
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			var e entry
			if uerr := json.Unmarshal(line, &e); uerr != nil {
				sink.Record(providerName, sessionID, deadletter.ParseError, uerr.Error(), line)
			} else {
				t.add(&e, line)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return t, err
		}
	}
	return t, nil
}

func (t *Transcript) add(e *entry, raw []byte) {
	t.entries = append(t.entries, e)
	t.raws = append(t.raws, raw)
	if e.Cwd != "" {
		t.Cwd = e.Cwd
	}
	// A mode change is recorded on the next prompt; permission-mode entries
	// are re-appended later with the current mode.
	if e.PermissionMode != "" {
		t.PermissionMode = e.PermissionMode
	}
	switch e.Type {
	case "custom-title":
		if e.CustomTitle != "" {
			t.Title = e.CustomTitle
		}
	case "ai-title":
		if e.AITitle != "" && t.Title == "" {
			t.Title = e.AITitle
		}
	}
	if e.Timestamp.After(t.Updated) {
		t.Updated = e.Timestamp
	}
	if e.Type == "user" && e.Message != nil {
		var s string
		if json.Unmarshal(e.Message.Content, &s) == nil {
			if m := modelChangeRe.FindStringSubmatch(s); m != nil {
				t.Model = m[1]
			}
		}
	}
	if e.Type == "attachment" && e.Attachment != nil && e.Attachment.Type == attachmentModel && e.Attachment.Identity != nil {
		t.ModelID = e.Attachment.Identity.ModelID
	}
	if e.Type == "assistant" && e.Message != nil {
		if m := e.Message.Model; m != "" && m != "<synthetic>" {
			t.Model = m
		}
		// Sidechains are subagents: they fill their own window, not this one.
		if !e.IsSidechain {
			if n := e.Message.Usage.total(); n > 0 {
				t.ContextTokens = n
			}
		}
		if e.Effort != "" {
			t.Effort = e.Effort
		}
		blocks, _ := decodeBlocks(e.Message.Content)
		for _, b := range blocks {
			if b.Type == "tool_use" {
				t.toolNames[b.ID] = b.Name
			}
		}
	}
}

// Messages converts the transcript into provider-neutral messages.
func (t *Transcript) Messages(opt ParseOptions) []model.Message {
	if opt.Sink == nil {
		opt.Sink = deadletter.Nop{}
	}
	results := map[string]toolResult{}
	for _, e := range t.entries {
		if e.Type != "user" || e.Message == nil {
			continue
		}
		blocks, ok := decodeBlocks(e.Message.Content)
		if !ok {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_result" {
				results[b.ToolUseID] = toolResult{block: b, entry: e}
			}
		}
	}
	// Tool calls still waiting for a result: the newest ones are the ones the
	// terminal is currently asking about.
	pending := pendingToolUses(t.entries, results)

	var out []model.Message
	for i, e := range t.entries {
		if e.IsSidechain {
			continue
		}
		msgs := t.convert(e, t.raws[i], results, pending, opt)
		out = append(out, msgs...)
	}
	// Only a running session can still pick up what is queued; in an ended
	// transcript leftovers are prompts the user never sent.
	if opt.Live != nil {
		out = append(out, queuedPrompts(t.entries)...)
	}
	return out
}

func (t *Transcript) convert(e *entry, raw []byte, results map[string]toolResult, pending map[string]bool, opt ParseOptions) []model.Message {
	switch {
	case e.Type == "user" || e.Type == "assistant":
		if e.Message == nil {
			opt.Sink.Record(providerName, opt.SessionID, deadletter.UnknownEvent, e.Type+" entry without message", raw)
			return []model.Message{fallback(e, "Unsupported event: "+e.Type+" without message")}
		}
		if e.Type == "user" {
			return t.convertUser(e, raw, results, opt)
		}
		return t.convertAssistant(e, raw, results, pending, opt)
	case e.Type == "system":
		return convertSystem(e, raw, opt)
	case e.Type == "attachment":
		return absorbedPrompt(e)
	case ignoredTypes[e.Type]:
		return nil
	default:
		// Unknown metadata lines are recorded but not shown: they are not
		// part of the conversation. Unknown lines carrying a message are shown.
		opt.Sink.Record(providerName, opt.SessionID, deadletter.UnknownEvent, "unknown entry type: "+e.Type, raw)
		if e.Message != nil {
			return []model.Message{fallback(e, "Unsupported event: "+e.Type)}
		}
		return nil
	}
}

func fallback(e *entry, text string) model.Message {
	return model.Message{ID: e.UUID, Role: model.RoleSystem, Timestamp: e.Timestamp, Blocks: []model.Block{model.TextBlock(text)}}
}

func decodeBlocks(raw json.RawMessage) ([]contentBlock, bool) {
	if len(raw) == 0 || raw[0] != '[' {
		return nil, false
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, false
	}
	return blocks, true
}

var (
	tagPattern       = regexp.MustCompile(`(?s)<([a-z-]+)>(.*?)</([a-z-]+)>`)
	commandNameRe    = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>`)
	commandArgsRe    = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
	answerPairRe     = regexp.MustCompile(`"([^"]*)"="([^"]*)"`)
	modelChangeRe    = regexp.MustCompile("^<local-command-stdout>Set model to `([^`]+)`")
	systemReminderRe = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
	taskSummaryRe    = regexp.MustCompile(`(?s)<summary>(.*?)</summary>`)
	taskEventRe      = regexp.MustCompile(`(?s)<event>(.*?)</event>`)
	anyTagRe         = regexp.MustCompile(`</?[a-z-]+>`)
)

// userText normalises the special XML-ish wrappers Claude Code writes for
// slash commands and local command output. Returns role and text; empty text
// means skip.
func userText(s string) (model.Role, string) {
	s = strings.TrimSpace(systemReminderRe.ReplaceAllString(s, ""))
	switch {
	case s == "":
		return "", ""
	case strings.HasPrefix(s, "<local-command-caveat>"):
		return "", ""
	case strings.HasPrefix(s, "<command-name>") || strings.HasPrefix(s, "<command-message>"):
		name := ""
		if m := commandNameRe.FindStringSubmatch(s); m != nil {
			name = strings.TrimSpace(m[1])
		}
		args := ""
		if m := commandArgsRe.FindStringSubmatch(s); m != nil {
			args = strings.TrimSpace(m[1])
		}
		return model.RoleUser, strings.TrimSpace(name + " " + args)
	case strings.HasPrefix(s, "<local-command-stdout>") || strings.HasPrefix(s, "<local-command-stderr>") ||
		strings.HasPrefix(s, "<bash-stdout>") || strings.HasPrefix(s, "<bash-stderr>"):
		inner := strings.TrimSpace(tagPattern.ReplaceAllString(s, "$2"))
		if inner == "" {
			return "", ""
		}
		return model.RoleTool, model.Truncate(inner, maxToolOutput)
	case strings.HasPrefix(s, "<bash-input>"):
		return model.RoleUser, "! " + strings.TrimSpace(tagPattern.ReplaceAllString(s, "$2"))
	case strings.HasPrefix(s, "<task-notification>"):
		return model.RoleSystem, model.Truncate(taskNotification(s), 500)
	}
	return model.RoleUser, s
}

// taskNotification keeps the human-readable part of a <task-notification>: the
// summary and, for Monitor notifications, the event lines. The task and tool
// ids, the output file path and the trailing instruction addressed to the agent
// are metadata and are dropped. tagPattern cannot be used here: it is
// non-greedy, so on a multi-tag payload it leaves the ids and a dangling
// </task-notification> in the text.
func taskNotification(s string) string {
	var parts []string
	if m := taskSummaryRe.FindStringSubmatch(s); m != nil {
		parts = append(parts, strings.TrimSpace(m[1]))
	}
	for _, m := range taskEventRe.FindAllStringSubmatch(s, -1) {
		parts = append(parts, strings.TrimSpace(m[1]))
	}
	if len(parts) == 0 {
		return strings.TrimSpace(anyTagRe.ReplaceAllString(s, ""))
	}
	return strings.Join(parts, "\n")
}

func (t *Transcript) convertUser(e *entry, raw []byte, results map[string]toolResult, opt ParseOptions) []model.Message {
	if e.IsMeta {
		return nil
	}
	c := e.Message.Content
	var str string
	if len(c) > 0 && c[0] == '"' {
		if err := json.Unmarshal(c, &str); err != nil {
			opt.Sink.Record(providerName, opt.SessionID, deadletter.ParseError, err.Error(), raw)
			return []model.Message{fallback(e, "Unsupported event: unreadable user message")}
		}
		role, text := userText(str)
		if text == "" {
			return nil
		}
		return []model.Message{{ID: e.UUID, Role: role, Timestamp: e.Timestamp, Blocks: []model.Block{model.TextBlock(text)}}}
	}
	blocks, ok := decodeBlocks(c)
	if !ok {
		opt.Sink.Record(providerName, opt.SessionID, deadletter.UnknownContent, "user content is neither string nor array", raw)
		return []model.Message{fallback(e, "Unsupported event: user content")}
	}

	var userBlocks, toolBlocks []model.Block
	role := model.RoleUser
	imageIndex := 0
	for _, b := range blocks {
		switch b.Type {
		case "text":
			r, text := userText(b.Text)
			if text == "" {
				continue
			}
			if r != model.RoleUser {
				role = r
			}
			userBlocks = append(userBlocks, model.TextBlock(text))
		case "image":
			userBlocks = append(userBlocks, model.Block{Type: model.BlockImage, URL: providers.ImageURL(opt.SessionID, e.UUID, imageIndex)})
			imageIndex++
		case "tool_result":
			if isAskUserQuestionResult(b, t, e) {
				continue // shown on the interaction itself
			}
			tb, n := toolResultBlocks(b, opt.SessionID, e.UUID, imageIndex)
			imageIndex += n
			toolBlocks = append(toolBlocks, tb...)
		default:
			opt.Sink.Record(providerName, opt.SessionID, deadletter.UnknownContent, "unsupported user content: "+b.Type, raw)
			userBlocks = append(userBlocks, model.TextBlock("Unsupported content: "+b.Type))
		}
	}
	var out []model.Message
	if len(userBlocks) > 0 {
		out = append(out, model.Message{ID: e.UUID, Role: role, Timestamp: e.Timestamp, Blocks: userBlocks})
	}
	if len(toolBlocks) > 0 {
		id := e.UUID
		if len(out) > 0 {
			id += "#tool"
		}
		out = append(out, model.Message{ID: id, Role: model.RoleTool, Timestamp: e.Timestamp, Blocks: toolBlocks})
	}
	return out
}

// isAskUserQuestionResult checks whether a tool_result answers an AskUserQuestion call.
func isAskUserQuestionResult(b contentBlock, t *Transcript, _ *entry) bool {
	return t.toolName(b.ToolUseID) == toolAskUserQuestion
}

func (t *Transcript) toolName(toolUseID string) string { return t.toolNames[toolUseID] }

// toolResultBlocks renders a tool_result; returns the number of images used.
func toolResultBlocks(b contentBlock, sessionID, messageID string, imageIndex int) ([]model.Block, int) {
	var out []model.Block
	used := 0
	text := ""
	if len(b.Content) > 0 && b.Content[0] == '"' {
		json.Unmarshal(b.Content, &text)
	} else if inner, ok := decodeBlocks(b.Content); ok {
		var parts []string
		for _, ib := range inner {
			switch ib.Type {
			case "text":
				parts = append(parts, ib.Text)
			case "image":
				out = append(out, model.Block{Type: model.BlockImage, URL: providers.ImageURL(sessionID, messageID, imageIndex+used)})
				used++
			default:
				parts = append(parts, "["+ib.Type+"]")
			}
		}
		text = strings.Join(parts, "\n")
	}
	text = strings.TrimSpace(text)
	if b.IsError {
		text = "Error: " + text
	}
	if text != "" {
		out = append([]model.Block{model.TextBlock(model.Truncate(text, maxToolOutput))}, out...)
	}
	return out, used
}

// absorbedPrompt renders a message the user sent while the agent was working.
// Claude Code queues such a prompt and, when it takes it into the running turn,
// records it as a queued_command attachment: it never becomes a user entry, so
// without this the message is missing from the conversation.
func absorbedPrompt(e *entry) []model.Message {
	if e.Attachment == nil || e.Attachment.Type != attachmentQueuedCommand {
		return nil // hooks, reminders and the other attachment kinds: metadata
	}
	role, text := userText(e.Attachment.Prompt)
	if text == "" {
		return nil
	}
	return []model.Message{{ID: e.UUID, Role: role, Timestamp: e.Timestamp, Blocks: []model.Block{model.TextBlock(text)}}}
}

// queuedPrompts returns the prompts still waiting in Claude Code's queue: sent
// while the agent was busy and not picked up yet. They are shown at the end of
// the conversation so a message sent from the app is visible right away; each
// is replaced by the real entry once the agent takes it.
func queuedPrompts(entries []*entry) []model.Message {
	absorbed := map[time.Time]bool{}
	for _, e := range entries {
		if e.Type == "attachment" && e.Attachment != nil && e.Attachment.Type == attachmentQueuedCommand {
			absorbed[e.Timestamp] = true
		}
	}
	var queue []*entry
	for _, e := range entries {
		if e.Type != "queue-operation" {
			continue
		}
		switch e.Operation {
		case "enqueue":
			queue = append(queue, e)
		case "dequeue", "remove":
			if i := queueIndex(queue, e.contentText()); i >= 0 {
				queue = append(queue[:i:i], queue[i+1:]...)
			}
		}
	}
	var out []model.Message
	for _, e := range queue {
		if absorbed[e.Timestamp] {
			continue // already shown from its attachment
		}
		role, text := userText(e.contentText())
		if text == "" {
			continue
		}
		out = append(out, model.Message{
			ID:        "queued:" + e.Timestamp.UTC().Format(time.RFC3339Nano),
			Role:      role,
			Timestamp: e.Timestamp,
			Blocks:    []model.Block{model.TextBlock(text)},
		})
	}
	return out
}

// queueIndex locates the entry a dequeue or remove refers to. The queue is
// FIFO and a dequeue carries no content, so it takes the head.
func queueIndex(queue []*entry, content string) int {
	if len(queue) == 0 {
		return -1
	}
	if content == "" {
		return 0
	}
	for i, e := range queue {
		if e.contentText() == content {
			return i
		}
	}
	return -1
}

// contentText decodes the plain string carried by system and queue-operation
// entries.
func (e *entry) contentText() string {
	var s string
	if len(e.Content) > 0 && e.Content[0] == '"' {
		json.Unmarshal(e.Content, &s)
	}
	return s
}

func convertSystem(e *entry, raw []byte, opt ParseOptions) []model.Message {
	text := e.contentText()
	switch {
	case e.Subtype == "local_command":
		_, t := userText(text)
		if t == "" {
			return nil
		}
		text = t
	case e.Subtype == "api_error" || e.IsAPIErrorMessage:
		if text == "" {
			text = "API error"
		}
	case e.Subtype == "compact_boundary":
		text = "Conversation compacted"
	case e.Subtype == "informational":
	case ignoredSystemSubtypes[e.Subtype]:
		return nil
	default:
		opt.Sink.Record(providerName, opt.SessionID, deadletter.UnknownEvent, "unknown system subtype: "+e.Subtype, raw)
		return nil
	}
	if strings.TrimSpace(text) == "" || e.IsMeta {
		return nil
	}
	return []model.Message{{ID: e.UUID, Role: model.RoleSystem, Timestamp: e.Timestamp, Blocks: []model.Block{model.TextBlock(model.Truncate(text, maxToolOutput))}}}
}

func (t *Transcript) convertAssistant(e *entry, raw []byte, results map[string]toolResult, pending map[string]bool, opt ParseOptions) []model.Message {
	blocks, ok := decodeBlocks(e.Message.Content)
	if !ok {
		var s string
		if json.Unmarshal(e.Message.Content, &s) == nil && s != "" {
			return []model.Message{{ID: e.UUID, Role: model.RoleAssistant, Timestamp: e.Timestamp, Blocks: textWithRefs(s, opt.Root)}}
		}
		opt.Sink.Record(providerName, opt.SessionID, deadletter.UnknownContent, "assistant content is neither string nor array", raw)
		return []model.Message{fallback(e, "Unsupported event: assistant content")}
	}
	role := model.RoleAssistant
	if e.IsAPIErrorMessage {
		role = model.RoleSystem
	}
	var out []model.Block
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) == "" {
				continue
			}
			out = append(out, textWithRefs(b.Text, opt.Root)...)
		case "thinking", "redacted_thinking":
			// Not user-facing.
		case "tool_use":
			res, answered := results[b.ID]
			out = append(out, t.toolUseBlocks(b, res, answered, pending[b.ID], raw, opt)...)
		case "server_tool_use", "web_search_tool_result", "web_fetch_tool_result":
			out = append(out, model.TextBlock("▸ "+b.Type+" "+b.Name))
		default:
			opt.Sink.Record(providerName, opt.SessionID, deadletter.UnknownContent, "unsupported assistant content: "+b.Type, raw)
			out = append(out, model.TextBlock("Unsupported content: "+b.Type))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return []model.Message{{ID: e.UUID, Role: role, Timestamp: e.Timestamp, Blocks: out}}
}

func textWithRefs(text, root string) []model.Block {
	return append([]model.Block{model.TextBlock(text)}, model.ExtractFileRefs(text, root)...)
}

// pendingToolUses returns tool_use ids without results in the last assistant
// turn (after the last real user prompt).
func pendingToolUses(entries []*entry, results map[string]toolResult) map[string]bool {
	out := map[string]bool{}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.IsSidechain || e.Message == nil {
			continue
		}
		if e.Type == "user" {
			if blocks, ok := decodeBlocks(e.Message.Content); ok && len(blocks) > 0 && blocks[0].Type == "tool_result" {
				continue
			}
			if !e.IsMeta {
				break // a new prompt: anything before is abandoned
			}
			continue
		}
		if e.Type != "assistant" {
			continue
		}
		blocks, _ := decodeBlocks(e.Message.Content)
		for _, b := range blocks {
			if b.Type == "tool_use" {
				if _, ok := results[b.ID]; !ok {
					out[b.ID] = true
				}
			}
		}
	}
	return out
}

type auqInput struct {
	Questions []struct {
		Question    string `json:"question"`
		Header      string `json:"header"`
		MultiSelect bool   `json:"multiSelect"`
		Options     []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"questions"`
}

func (t *Transcript) toolUseBlocks(b contentBlock, res toolResult, answered, pending bool, raw []byte, opt ParseOptions) []model.Block {
	var in map[string]any
	json.Unmarshal(b.Input, &in)
	str := func(k string) string { s, _ := in[k].(string); return s }

	state := model.InteractionAnswered
	if !answered {
		state = model.InteractionClosed
		if pending && opt.Live.Blocked() {
			state = model.InteractionPending
		}
	}

	switch b.Name {
	case toolAskUserQuestion:
		ia, err := askUserQuestionInteraction(b)
		if err != nil {
			opt.Sink.Record(providerName, opt.SessionID, deadletter.UnsupportedInteraction, err.Error(), raw)
			return []model.Block{model.TextBlock("Unsupported event: AskUserQuestion (" + err.Error() + ")")}
		}
		ia.State = state
		if answered {
			ia.Answer = answerSummary(res)
		}
		return []model.Block{{Type: model.BlockInteraction, Interaction: ia}}
	}

	var blocks []model.Block
	switch b.Name {
	case "Bash":
		text := "$ " + str("command")
		if d := str("description"); d != "" {
			text = d + "\n" + text
		}
		blocks = append(blocks, model.TextBlock(text))
	case "Read", "Edit", "MultiEdit", "Write", "NotebookEdit":
		p := str("file_path")
		if p == "" {
			p = str("notebook_path")
		}
		blocks = append(blocks, model.TextBlock(b.Name+" "+model.DisplayPath(opt.Root, p)))
		line := 0
		if off, ok := in["offset"].(float64); ok {
			line = int(off)
		}
		if fb, ok := model.FileRef(opt.Root, p, line); ok {
			blocks = append(blocks, fb)
		}
	case "Glob", "Grep":
		blocks = append(blocks, model.TextBlock(b.Name+" "+str("pattern")))
	case "WebFetch":
		blocks = append(blocks, model.TextBlock("WebFetch "+str("url")))
	case "WebSearch":
		blocks = append(blocks, model.TextBlock("WebSearch "+str("query")))
	case "Task", "Agent":
		blocks = append(blocks, model.TextBlock("Agent: "+str("description")))
	case "TodoWrite":
		blocks = append(blocks, model.TextBlock(todoText(in)))
	case "ExitPlanMode":
		blocks = append(blocks, model.TextBlock("Plan:\n"+str("plan")))
	case "ToolSearch", "Skill", "SlashCommand", "BashOutput", "KillShell", "KillBash", "TaskOutput", "TaskStop",
		"Monitor", "SendUserFile", "ListMcpResourcesTool", "ReadMcpResourceTool", "EnterPlanMode", "LSP", "SendMessage":
		blocks = append(blocks, model.TextBlock("▸ "+b.Name+" "+compactJSON(b.Input, 200)))
	default:
		if !strings.HasPrefix(b.Name, "mcp__") {
			opt.Sink.Record(providerName, opt.SessionID, deadletter.UnknownContent, "unsupported tool_use: "+b.Name, raw)
		}
		blocks = append(blocks, model.TextBlock("▸ "+b.Name+" "+compactJSON(b.Input, 200)))
	}

	if state == model.InteractionPending {
		blocks = append(blocks, model.Block{Type: model.BlockInteraction, Interaction: &model.Interaction{
			ID:        b.ID,
			Type:      model.InteractionApproval,
			State:     state,
			Title:     "Allow " + b.Name + "?",
			Detail:    model.Truncate(approvalDetail(b.Name, in, b.Input), 1000),
			Supported: true,
			Decisions: []string{model.DecisionApprove, model.DecisionDeny},
		}})
	}
	return blocks
}

func askUserQuestionInteraction(b contentBlock) (*model.Interaction, error) {
	var in auqInput
	if err := json.Unmarshal(b.Input, &in); err != nil {
		return nil, fmt.Errorf("invalid AskUserQuestion input: %w", err)
	}
	if len(in.Questions) == 0 {
		return nil, fmt.Errorf("AskUserQuestion without questions")
	}
	ia := &model.Interaction{ID: b.ID, Type: model.InteractionQuestions, Title: "Claude needs input", Supported: true}
	for i, q := range in.Questions {
		mq := model.Question{ID: fmt.Sprint(i), Type: model.QuestionSelect, Header: q.Header, Question: q.Question, AllowOther: true}
		if q.MultiSelect {
			mq.Type = model.QuestionMultiSelect
		}
		for _, o := range q.Options {
			mq.Options = append(mq.Options, model.Option{Label: o.Label, Description: o.Description})
		}
		if len(mq.Options) == 0 {
			mq.Type = model.QuestionText
		}
		ia.Questions = append(ia.Questions, mq)
	}
	return ia, nil
}

func answerSummary(res toolResult) string {
	text := ""
	if len(res.block.Content) > 0 && res.block.Content[0] == '"' {
		json.Unmarshal(res.block.Content, &text)
	} else if inner, ok := decodeBlocks(res.block.Content); ok {
		for _, ib := range inner {
			text += ib.Text
		}
	}
	if res.block.IsError {
		return "Not answered"
	}
	// The result text is prose around `"question"="answer"` pairs; keep the pairs.
	var pairs []string
	for _, m := range answerPairRe.FindAllStringSubmatch(text, -1) {
		pairs = append(pairs, m[1]+" → "+m[2])
	}
	if len(pairs) > 0 {
		return model.Truncate(strings.Join(pairs, "\n"), 500)
	}
	return model.Truncate(text, 500)
}

func todoText(in map[string]any) string {
	todos, _ := in["todos"].([]any)
	var lines []string
	for _, t := range todos {
		m, _ := t.(map[string]any)
		mark := "☐"
		switch m["status"] {
		case "completed":
			mark = "☑"
		case "in_progress":
			mark = "▶"
		}
		c, _ := m["content"].(string)
		lines = append(lines, mark+" "+c)
	}
	return "Todos\n" + strings.Join(lines, "\n")
}

func approvalDetail(name string, in map[string]any, raw json.RawMessage) string {
	if c, ok := in["command"].(string); ok {
		return c
	}
	if p, ok := in["file_path"].(string); ok {
		return p
	}
	if u, ok := in["url"].(string); ok {
		return u
	}
	return name + " " + compactJSON(raw, 800)
}

func compactJSON(raw json.RawMessage, n int) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return model.Truncate(string(raw), n)
	}
	return model.Truncate(buf.String(), n)
}

// imageAt returns the index-th image (user images first, then tool_result
// images, matching the numbering used when building URLs).
func (t *Transcript) imageAt(messageID string, index int) (*imageSource, bool) {
	uuid := strings.TrimSuffix(messageID, "#tool")
	for _, e := range t.entries {
		if e.UUID != uuid || e.Message == nil {
			continue
		}
		blocks, _ := decodeBlocks(e.Message.Content)
		var imgs []*imageSource
		for _, b := range blocks {
			if b.Type == "image" && b.Source != nil {
				imgs = append(imgs, b.Source)
			}
		}
		for _, b := range blocks {
			if b.Type != "tool_result" || t.toolName(b.ToolUseID) == toolAskUserQuestion {
				continue
			}
			if inner, ok := decodeBlocks(b.Content); ok {
				for _, ib := range inner {
					if ib.Type == "image" && ib.Source != nil {
						imgs = append(imgs, ib.Source)
					}
				}
			}
		}
		if index >= 0 && index < len(imgs) {
			return imgs[index], true
		}
	}
	return nil, false
}

// summary extracts list information.
func (t *Transcript) summary(opt ParseOptions) providers.Summary {
	s := providers.Summary{Cwd: t.Cwd, Title: t.Title, UpdatedAt: t.Updated,
		Model: t.Model, Effort: t.Effort, Mode: modeLabel(t.PermissionMode),
		Context: model.NewContextUsage(t.ContextTokens, contextWindow(t.ModelID, t.Model))}
	msgs := t.Messages(ParseOptions{SessionID: opt.SessionID, Live: opt.Live, Sink: deadletter.Nop{}})
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != model.RoleAssistant && m.Role != model.RoleUser {
			continue
		}
		for _, b := range m.Blocks {
			if b.Type == model.BlockText && s.LastMessage == "" {
				s.LastMessage = providers.OneLine(b.Text, 160)
			}
			if b.Type == model.BlockInteraction && b.Interaction.State == model.InteractionPending && s.Pending == "" {
				s.Pending = b.Interaction.Type
			}
		}
		if s.LastMessage != "" {
			break
		}
	}
	// A turn that ended with an API error.
	for i := len(t.entries) - 1; i >= 0; i-- {
		e := t.entries[i]
		if e.Type == "assistant" {
			s.LastTurnFailed = e.IsAPIErrorMessage
			break
		}
		if e.Type == "user" && !e.IsMeta && e.Message != nil {
			if blocks, ok := decodeBlocks(e.Message.Content); !ok || len(blocks) == 0 || blocks[0].Type != "tool_result" {
				break
			}
		}
	}
	if s.Pending == "" {
		// Pending may be on an earlier message than the last text.
		for _, m := range msgs {
			for _, b := range m.Blocks {
				if b.Type == model.BlockInteraction && b.Interaction.State == model.InteractionPending {
					s.Pending = b.Interaction.Type
				}
			}
		}
	}
	return s
}

// Replay re-parses one raw transcript line with the current parser. Used by
// the dead-letter replay command.
func Replay(raw []byte) ([]model.Message, []deadletter.Entry) {
	rec := &deadletter.Recorder{}
	t, _ := Decode(bytes.NewReader(raw), "claude:replay", rec)
	msgs := t.Messages(ParseOptions{SessionID: "claude:replay", Sink: rec})
	return msgs, rec.Entries
}

// modeLabel names Claude Code permission modes like its status line does.
func modeLabel(mode string) string {
	switch mode {
	case "":
		return ""
	case "default", "manual":
		return "Default"
	case "acceptEdits":
		return "Accept edits"
	case "plan":
		return "Plan"
	case "auto":
		return "Auto"
	case "bypassPermissions":
		return "Bypass permissions"
	case "dontAsk":
		return "Don't ask"
	}
	return mode
}
