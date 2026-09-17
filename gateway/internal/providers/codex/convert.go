package codex

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

// Thread mirrors the parts of app-server v2 Thread we use.
type Thread struct {
	ID        string          `json:"id"`
	Preview   string          `json:"preview"`
	Name      *string         `json:"name"`
	Cwd       string          `json:"cwd"`
	Path      string          `json:"path"` // rollout file, the only source of token counts
	Model     string          `json:"model"`
	Effort    *string         `json:"reasoningEffort"`
	CreatedAt int64           `json:"createdAt"`
	Ephemeral bool            `json:"ephemeral"`
	UpdatedAt int64           `json:"updatedAt"`
	Status    ThreadStatus    `json:"status"`
	Source    json.RawMessage `json:"source"`
	Turns     []Turn          `json:"turns"`
}

type ThreadStatus struct {
	Type        string   `json:"type"` // notLoaded | idle | systemError | active
	ActiveFlags []string `json:"activeFlags"`
}

type Turn struct {
	ID        string            `json:"id"`
	Status    string            `json:"status"` // completed | interrupted | failed | inProgress
	Error     *TurnError        `json:"error"`
	StartedAt *int64            `json:"startedAt"`
	Items     []json.RawMessage `json:"items"`
}

type TurnError struct {
	Message string `json:"message"`
}

type itemHead struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type userInput struct {
	Type string `json:"type"`
	Text string `json:"text"`
	URL  string `json:"url"`
	Path string `json:"path"`
	Name string `json:"name"`
}

type commandAction struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Path    string `json:"path"`
}

type item struct {
	Type string `json:"type"`
	ID   string `json:"id"`

	Content   []userInput     `json:"content"` // userMessage
	Text      string          `json:"text"`    // agentMessage, plan
	Delivery  string          `json:"delivery"`
	Questions []asyncQuestion `json:"questions"`

	Command          string          `json:"command"`
	CommandActions   []commandAction `json:"commandActions"`
	AggregatedOutput *string         `json:"aggregatedOutput"`
	ExitCode         *int            `json:"exitCode"`
	Status           string          `json:"status"`

	Changes []struct {
		Path string          `json:"path"`
		Kind json.RawMessage `json:"kind"`
		Diff string          `json:"diff"`
	} `json:"changes"`

	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Result    json.RawMessage `json:"result"`
	Error     json.RawMessage `json:"error"`

	Name   string          `json:"name"`
	Output json.RawMessage `json:"output"`

	Prompt string `json:"prompt"`
	Query  string `json:"query"`
	Path   string `json:"path"`

	SavedPath *string `json:"savedPath"`
}

const maxToolOutput = 1500

type convertOptions struct {
	SessionID string
	Root      string
	Sink      deadletter.Sink
}

// ConvertThread flattens turns into messages. Unknown items become text
// fallbacks and are dead-lettered with their raw JSON.
func ConvertThread(th *Thread, opt convertOptions) []model.Message {
	if opt.Sink == nil {
		opt.Sink = deadletter.Nop{}
	}
	var out []model.Message
	for _, turn := range th.Turns {
		ts := time.Time{}
		if turn.StartedAt != nil {
			ts = time.Unix(*turn.StartedAt, 0).UTC()
		}
		for _, raw := range turn.Items {
			if m, ok := convertItem(raw, ts, opt); ok {
				out = append(out, m)
			}
		}
		if turn.Status == "failed" {
			text := "Turn failed"
			if turn.Error != nil && turn.Error.Message != "" {
				text += ": " + errorText(turn.Error.Message)
			}
			out = append(out, model.Message{ID: turn.ID + "#error", Role: model.RoleSystem, Timestamp: ts, Blocks: []model.Block{model.TextBlock(text)}})
		}
	}
	return out
}

// errorText unwraps upstream JSON error bodies into their message.
func errorText(s string) string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(s), &body) == nil && body.Error.Message != "" {
		return body.Error.Message
	}
	return s
}

func convertItem(raw json.RawMessage, ts time.Time, opt convertOptions) (model.Message, bool) {
	var it item
	if err := json.Unmarshal(raw, &it); err != nil {
		opt.Sink.Record(providerName, opt.SessionID, deadletter.ParseError, err.Error(), raw)
		var head itemHead
		json.Unmarshal(raw, &head)
		return model.Message{ID: head.ID, Role: model.RoleSystem, Timestamp: ts, Blocks: []model.Block{model.TextBlock("Unsupported event: unreadable item")}}, head.ID != ""
	}
	msg := func(role model.Role, blocks ...model.Block) (model.Message, bool) {
		return model.Message{ID: it.ID, Role: role, Timestamp: ts, Blocks: blocks}, len(blocks) > 0
	}
	switch it.Type {
	case "userMessage":
		var blocks []model.Block
		for i, in := range it.Content {
			switch in.Type {
			case "text":
				if strings.TrimSpace(in.Text) != "" {
					blocks = append(blocks, model.TextBlock(in.Text))
				}
			case "image":
				if strings.HasPrefix(in.URL, "data:") {
					blocks = append(blocks, model.Block{Type: model.BlockImage, URL: providers.ImageURL(opt.SessionID, it.ID, i)})
				} else if in.URL != "" {
					blocks = append(blocks, model.Block{Type: model.BlockImage, URL: in.URL})
				}
			case "localImage":
				blocks = append(blocks, model.Block{Type: model.BlockImage, URL: providers.FileImageURL(opt.SessionID, model.DisplayPath(opt.Root, in.Path))})
			case "skill", "mention":
				blocks = append(blocks, model.TextBlock("$"+in.Name))
			default:
				opt.Sink.Record(providerName, opt.SessionID, deadletter.UnknownContent, "unsupported user input: "+in.Type, raw)
				blocks = append(blocks, model.TextBlock("Unsupported content: "+in.Type))
			}
		}
		return msg(model.RoleUser, blocks...)
	case "agentMessage":
		if strings.TrimSpace(it.Text) == "" {
			return model.Message{}, false
		}
		return msg(model.RoleAssistant, append([]model.Block{model.TextBlock(it.Text)}, model.ExtractFileRefs(it.Text, opt.Root)...)...)
	case "plan":
		return msg(model.RoleAssistant, model.TextBlock("Plan:\n"+it.Text))
	case "reasoning", "hookPrompt", "sleep", "subAgentActivity":
		return model.Message{}, false
	case "commandExecution":
		cmd := it.Command
		if len(it.CommandActions) == 1 && it.CommandActions[0].Command != "" {
			cmd = it.CommandActions[0].Command
		}
		text := "$ " + cmd
		if it.AggregatedOutput != nil && strings.TrimSpace(*it.AggregatedOutput) != "" {
			text += "\n" + model.Truncate(strings.TrimRight(*it.AggregatedOutput, "\n"), maxToolOutput)
		}
		switch {
		case it.Status == "declined":
			text += "\n(declined)"
		case it.ExitCode != nil && *it.ExitCode != 0:
			text += fmt.Sprintf("\n(exit %d)", *it.ExitCode)
		}
		blocks := []model.Block{model.TextBlock(text)}
		for _, a := range it.CommandActions {
			if a.Type == "read" && a.Path != "" {
				if fb, ok := model.FileRef(opt.Root, a.Path, 0); ok {
					blocks = append(blocks, fb)
				}
			}
		}
		return msg(model.RoleTool, blocks...)
	case "fileChange":
		var parts []string
		var files []model.Block
		for _, ch := range it.Changes {
			parts = append(parts, fmt.Sprintf("%s %s\n%s", changeKind(ch.Kind), model.DisplayPath(opt.Root, ch.Path), strings.TrimRight(ch.Diff, "\n")))
			if fb, ok := model.FileRef(opt.Root, ch.Path, 0); ok {
				files = append(files, fb)
			}
		}
		text := model.Truncate(strings.Join(parts, "\n"), maxToolOutput)
		if it.Status == "declined" {
			text += "\n(declined)"
		}
		return msg(model.RoleTool, append([]model.Block{model.TextBlock(text)}, files...)...)
	case "mcpToolCall":
		text := fmt.Sprintf("▸ %s.%s %s", it.Server, it.Tool, compact(it.Arguments, 200))
		if r := mcpResultText(it.Result); r != "" {
			text += "\n" + model.Truncate(r, maxToolOutput)
		}
		if len(it.Error) > 0 && string(it.Error) != "null" {
			text += "\nError: " + compact(it.Error, 300)
		}
		return msg(model.RoleTool, model.TextBlock(text))
	case "dynamicToolCall":
		return msg(model.RoleTool, model.TextBlock(fmt.Sprintf("▸ %s %s", it.Tool, compact(it.Arguments, 200))))
	case "collabAgentToolCall":
		return msg(model.RoleTool, model.TextBlock(fmt.Sprintf("▸ agent %s: %s", it.Tool, providers.OneLine(it.Prompt, 200))))
	case "functionCallOutput":
		return msg(model.RoleTool, model.TextBlock(fmt.Sprintf("▸ %s\n%s", it.Name, model.Truncate(compact(it.Output, maxToolOutput), maxToolOutput))))
	case "webSearch":
		return msg(model.RoleTool, model.TextBlock("WebSearch "+it.Query))
	case "imageView":
		return msg(model.RoleTool, model.Block{Type: model.BlockImage, URL: providers.FileImageURL(opt.SessionID, model.DisplayPath(opt.Root, it.Path))})
	case "imageGeneration":
		if it.SavedPath != nil && *it.SavedPath != "" {
			return msg(model.RoleAssistant, model.Block{Type: model.BlockImage, URL: providers.FileImageURL(opt.SessionID, model.DisplayPath(opt.Root, *it.SavedPath))})
		}
		return msg(model.RoleTool, model.TextBlock("Image generation "+it.Status))
	case "enteredReviewMode":
		return msg(model.RoleSystem, model.TextBlock("Review started"))
	case "exitedReviewMode":
		return msg(model.RoleSystem, model.TextBlock("Review finished"))
	case "contextCompaction":
		return msg(model.RoleSystem, model.TextBlock("Conversation compacted"))
	default:
		opt.Sink.Record(providerName, opt.SessionID, deadletter.UnknownContent, "unsupported thread item: "+it.Type, raw)
		return msg(model.RoleSystem, model.TextBlock("Unsupported event: "+it.Type))
	}
}

func changeKind(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Type != "" {
		return obj.Type
	}
	return "change"
}

func mcpResultText(raw json.RawMessage) string {
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return ""
	}
	var parts []string
	for _, c := range r.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func compact(raw json.RawMessage, n int) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return model.Truncate(s, n)
	}
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return model.Truncate(string(raw), n)
	}
	return model.Truncate(buf.String(), n)
}

// dataURLImage finds the index-th input of a user message and decodes it
// when it is a data: URL.
func dataURLImage(th *Thread, messageID string, index int) (string, []byte, bool) {
	for _, turn := range th.Turns {
		for _, raw := range turn.Items {
			var it item
			if json.Unmarshal(raw, &it) != nil || it.ID != messageID || it.Type != "userMessage" {
				continue
			}
			if index < 0 || index >= len(it.Content) {
				return "", nil, false
			}
			u := it.Content[index].URL
			meta, data, ok := strings.Cut(strings.TrimPrefix(u, "data:"), ",")
			if !strings.HasPrefix(u, "data:") || !ok || !strings.HasSuffix(meta, ";base64") {
				return "", nil, false
			}
			b, err := base64.StdEncoding.DecodeString(data)
			if err != nil {
				return "", nil, false
			}
			return strings.TrimSuffix(meta, ";base64"), b, true
		}
	}
	return "", nil, false
}

// lastTurn returns the latest turn or nil.
func lastTurn(th *Thread) *Turn {
	if len(th.Turns) == 0 {
		return nil
	}
	return &th.Turns[len(th.Turns)-1]
}

func lastText(msgs []model.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != model.RoleAssistant && m.Role != model.RoleUser {
			continue
		}
		for _, b := range m.Blocks {
			if b.Type == model.BlockText {
				return providers.OneLine(b.Text, 160)
			}
		}
	}
	return ""
}

// Replay re-converts one raw thread item (dead-letter replay).
func Replay(raw []byte) ([]model.Message, []deadletter.Entry) {
	rec := &deadletter.Recorder{}
	th := &Thread{Turns: []Turn{{Items: []json.RawMessage{raw}}}}
	return ConvertThread(th, convertOptions{SessionID: "codex:replay", Sink: rec}), rec.Entries
}
