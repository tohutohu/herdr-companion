// Package devin adapts the local Devin CLI to Herdr Companion.
//
// Devin keeps its local conversation forest in a shared SQLite database. The
// adapter only opens that database read-only; prompts and resumptions still go
// through the Herdr PTY so the running Devin TUI remains the source of truth.
package devin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/herdr"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

const providerName = "devin"

type Config struct {
	Database string `json:"database,omitempty"`
	Binary   string `json:"binary,omitempty"`
}

type Provider struct {
	cfg    Config
	dbPath string
	term   providers.Terminal
	sink   deadletter.Sink
}

// DefaultBinary also checks Devin CLI's installer location because the
// menu-bar Gateway is launched by macOS without the shell's PATH.
func DefaultBinary() string {
	if binary := strings.TrimSpace(os.Getenv("DEVIN_BINARY")); binary != "" {
		return binary
	}
	if binary, err := exec.LookPath("devin"); err == nil {
		return binary
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, path := range []string{
			filepath.Join(home, ".local", "bin", "devin"),
			filepath.Join(home, "bin", "devin"),
		} {
			if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				return path
			}
		}
	}
	return "devin"
}

func New(cfg Config, term providers.Terminal, sink deadletter.Sink) *Provider {
	if cfg.Database == "" {
		cfg.Database = defaultDatabase()
	}
	if cfg.Binary == "" {
		cfg.Binary = DefaultBinary()
	}
	if sink == nil {
		sink = deadletter.Nop{}
	}
	return &Provider{cfg: cfg, dbPath: cfg.Database, term: term, sink: sink}
}

func (*Provider) Name() string        { return providerName }
func (*Provider) DisplayName() string { return "Devin" }
func (*Provider) HerdrAgent() string  { return providerName }

var modelFamilyPattern = regexp.MustCompile(`^(.+?)\s+\(([A-Za-z0-9][A-Za-z0-9._-]{0,99})\)$`)

func (p *Provider) Models(ctx context.Context) (providers.ModelCatalog, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, p.cfg.Binary, "models", "list").Output()
	if err != nil {
		return providers.ModelCatalog{}, fmt.Errorf("Devin models: %w", err)
	}
	cat := providers.ModelCatalog{Models: []providers.ModelOption{}}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		m := modelFamilyPattern.FindStringSubmatch(line)
		if len(m) != 3 || !providers.ValidModelID(m[2]) || seen[m[2]] {
			continue
		}
		seen[m[2]] = true
		cat.Models = append(cat.Models, providers.ModelOption{ID: m[2], Name: strings.TrimSpace(m[1])})
	}
	cat.Modes = devinModes
	return cat, nil
}

var devinModes = []providers.ModeOption{
	{ID: "auto", Name: "Auto", Description: "Auto-approves read-only tools", Default: true},
	{ID: "accept-edits", Name: "Accept edits", Description: "Also approves edits inside the workspace"},
	{ID: "smart", Name: "Smart", Description: "Lets Devin judge safe actions automatically"},
}

func (p *Provider) LaunchArgs(opts providers.LaunchOptions) []string {
	args := []string{}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.Mode != "" {
		args = append(args, "--permission-mode", opts.Mode)
	}
	return args
}

func (*Provider) ResumeArgs(id, cwd string) []string { return []string{"--resume", id} }

// Devin's workspace trust screen is version-dependent. Returning nil leaves
// unknown screens visible in the terminal, where Continue can safely retry;
// accepting a guessed dialog could grant access to the wrong directory.
func (*Provider) StartupKeys(string) []string { return nil }

func (p *Provider) Summary(ctx context.Context, nativeID string, live *providers.Live) (*providers.Summary, error) {
	row, c, err := p.history(ctx, nativeID)
	if err != nil {
		return nil, err
	}
	s := summaryFromRow(row)
	s.LastTurnFailed = c.Failed
	for i := len(c.Messages) - 1; i >= 0; i-- {
		for _, block := range c.Messages[i].Blocks {
			if block.Type == model.BlockText && strings.TrimSpace(block.Text) != "" {
				s.LastMessage = providers.OneLine(block.Text, 200)
				break
			}
		}
		if s.LastMessage != "" {
			break
		}
	}
	if c.Context != nil {
		s.Context = c.Context
	}
	if live != nil && live.Blocked() {
		s.Pending = model.InteractionQuestions
		s.Status = model.StatusWaitingInput
	}
	return &s, nil
}

func (p *Provider) Messages(ctx context.Context, nativeID string, live *providers.Live) ([]model.Message, error) {
	_, c, err := p.history(ctx, nativeID)
	if err != nil {
		return nil, err
	}
	if live != nil && live.Blocked() {
		c.Messages = append(c.Messages, model.Message{
			ID:   providerName + "-terminal",
			Role: model.RoleAssistant,
			Blocks: []model.Block{{Type: model.BlockInteraction, Interaction: &model.Interaction{
				ID:        providerName + "-terminal",
				Type:      model.InteractionQuestions,
				State:     model.InteractionPending,
				Title:     "Devin is waiting for input",
				Detail:    "Open the terminal to answer this Devin dialog.",
				Supported: false,
			}}},
		})
	}
	return c.Messages, nil
}

func (p *Provider) Image(ctx context.Context, nativeID, messageID string, index int) (string, []byte, error) {
	_, c, err := p.history(ctx, nativeID)
	if err != nil {
		return "", nil, err
	}
	images := c.Images[messageID]
	if index < 0 || index >= len(images) {
		return "", nil, providers.ErrNotFound
	}
	return images[index].Mime, images[index].Data, nil
}

func (p *Provider) Send(ctx context.Context, nativeID string, live *providers.Live, in model.Input) error {
	if live == nil {
		return providers.ErrNotLive
	}
	if live.Blocked() {
		return &herdr.Error{Code: "agent_blocked", Message: "Devin is waiting for terminal input"}
	}
	if strings.TrimSpace(in.Text) == "" && len(in.Images) == 0 && len(in.Files) == 0 {
		return errors.New("empty message")
	}
	return p.term.Prompt(ctx, live.PaneID, promptText(in))
}

func promptText(in model.Input) string {
	text := providers.TextWithFiles(in)
	for _, image := range in.Images {
		text = strings.TrimSpace(text + "\n" + image)
	}
	return text
}

func (*Provider) Respond(context.Context, string, *providers.Live, model.InteractionResponse) error {
	return providers.ErrUnsupported
}

// Replay re-runs the Devin message parser over one raw chat_message saved in
// a dead letter. Devin's dead-letter records contain the original JSON rather
// than a database row wrapper, so a synthetic node is sufficient here.
func Replay(raw []byte) ([]model.Message, []deadletter.Entry) {
	sink := &deadletter.Recorder{}
	p := New(Config{}, nil, sink)
	msg, _, _, _ := p.convertNode("replay", "", node{ID: 1, Raw: string(raw)})
	if msg.ID == "" || len(msg.Blocks) == 0 {
		return nil, sink.Entries
	}
	return []model.Message{msg}, sink.Entries
}

type inlineImage struct {
	Mime string
	Data []byte
}

type conversion struct {
	Messages []model.Message
	Images   map[string][]inlineImage
	Failed   bool
	Context  *model.ContextUsage
}

func (p *Provider) history(ctx context.Context, id string) (sessionRow, conversion, error) {
	if !validSessionID(id) {
		return sessionRow{}, conversion{}, providers.ErrNotFound
	}
	db, err := p.database()
	if err != nil {
		return sessionRow{}, conversion{}, err
	}
	defer db.Close()
	row, err := readSession(ctx, db, id)
	if err != nil {
		return sessionRow{}, conversion{}, err
	}
	nodes, err := readNodes(ctx, db, id)
	if err != nil {
		return sessionRow{}, conversion{}, err
	}
	return row, p.convert(id, row, nodes), nil
}

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func validSessionID(id string) bool { return sessionIDPattern.MatchString(id) }

func messageID(nodeID int64) string { return "node-" + strconv.FormatInt(nodeID, 10) }

func gatewayID(nativeID string) string { return providerName + ":" + nativeID }

func (p *Provider) convert(id string, row sessionRow, nodes []node) conversion {
	out := conversion{Messages: []model.Message{}, Images: map[string][]inlineImage{}}
	byID := make(map[int64]node, len(nodes))
	var newest int64
	for _, n := range nodes {
		byID[n.ID] = n
		if n.ID > newest {
			newest = n.ID
		}
	}
	tip := newest
	if row.MainNode.Valid {
		tip = row.MainNode.Int64
	}
	if tip == 0 {
		return out
	}
	var chain []node
	seen := map[int64]bool{}
	for tip != 0 && !seen[tip] {
		seen[tip] = true
		n, ok := byID[tip]
		if !ok {
			p.sink.Record(providerName, gatewayID(id), deadletter.ProviderError, "active Devin message chain is incomplete", map[string]any{"nodeId": tip})
			break
		}
		chain = append(chain, n)
		if !n.Parent.Valid {
			break
		}
		tip = n.Parent.Int64
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	for _, n := range chain {
		msg, failed, images, contextUsage := p.convertNode(id, row.Cwd, n)
		if failed {
			out.Failed = true
		}
		if msg.ID != "" && len(msg.Blocks) > 0 {
			out.Messages = append(out.Messages, msg)
		}
		if len(images) > 0 {
			out.Images[msg.ID] = images
		}
		if contextUsage != nil {
			out.Context = contextUsage
		}
		if msg.Role == model.RoleUser {
			out.Failed = false
		}
	}
	return out
}

func (p *Provider) convertNode(sessionID, root string, n node) (model.Message, bool, []inlineImage, *model.ContextUsage) {
	id := messageID(n.ID)
	msg := model.Message{ID: id, Timestamp: timestamp(n.Created), Role: model.RoleSystem, Blocks: []model.Block{}}
	var raw any
	if err := json.Unmarshal([]byte(n.Raw), &raw); err != nil {
		p.sink.Record(providerName, gatewayID(sessionID), deadletter.ParseError, "invalid Devin chat message", n.Raw)
		msg.Blocks = append(msg.Blocks, model.TextBlock("Devin message unavailable: invalid JSON"))
		return msg, true, nil, nil
	}
	payload := unwrapPayload(raw)
	role := strings.ToLower(stringValue(payload["role"]))
	if role == "" {
		role = strings.ToLower(stringValue(payload["type"]))
	}
	switch role {
	case "user":
		msg.Role = model.RoleUser
	case "assistant", "model":
		msg.Role = model.RoleAssistant
	case "tool", "function":
		msg.Role = model.RoleTool
	case "system":
		msg.Role = model.RoleSystem
	default:
		p.sink.Record(providerName, gatewayID(sessionID), deadletter.UnknownEvent, "unsupported Devin message role "+role, n.Raw)
		msg.Blocks = append(msg.Blocks, model.TextBlock("Devin message unavailable: unsupported role"))
		return msg, true, nil, nil
	}
	collector := contentCollector{}
	if content, ok := payload["content"]; ok {
		collectContent(content, &collector)
	}
	if text := stringValue(payload["text"]); text != "" {
		collector.Texts = appendIfMissing(collector.Texts, text)
	}
	if calls, ok := payload["tool_calls"]; ok {
		collectToolCalls(calls, &collector)
	}
	if calls, ok := payload["toolCalls"]; ok {
		collectToolCalls(calls, &collector)
	}
	if images, ok := payload["images"]; ok {
		collectImages(images, &collector)
	}
	for _, text := range collector.Texts {
		if strings.TrimSpace(text) == "" {
			continue
		}
		msg.Blocks = append(msg.Blocks, model.TextBlock(text))
		if msg.Role == model.RoleAssistant {
			msg.Blocks = append(msg.Blocks, model.ExtractFileRefs(text, root)...)
		}
	}
	images := make([]inlineImage, 0, len(collector.Images))
	for _, img := range collector.Images {
		if len(img.Data) > 0 {
			imageIndex := len(images)
			images = append(images, inlineImage{Mime: img.Mime, Data: img.Data})
			msg.Blocks = append(msg.Blocks, model.Block{Type: model.BlockImage, URL: providers.ImageURL(sessionID, id, imageIndex)})
		} else if img.Path != "" {
			msg.Blocks = append(msg.Blocks, model.Block{Type: model.BlockImage, URL: providers.FileImageURL(sessionID, img.Path)})
		}
	}
	failed := failureValue(payload)
	var metadata any
	if n.Metadata.Valid && json.Unmarshal([]byte(n.Metadata.String), &metadata) == nil {
		failed = failed || failureValue(metadata)
	}
	return msg, failed, images, contextUsage(payload, metadata)
}

func unwrapPayload(raw any) map[string]any {
	obj, _ := raw.(map[string]any)
	if obj == nil {
		return map[string]any{}
	}
	if _, hasRole := obj["role"]; !hasRole {
		if nested, ok := obj["message"].(map[string]any); ok {
			return nested
		}
	}
	return obj
}

type imageRef struct {
	Mime string
	Data []byte
	Path string
}

type contentCollector struct {
	Texts  []string
	Images []imageRef
}

func collectContent(value any, out *contentCollector) {
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			out.Texts = append(out.Texts, v)
		}
	case []any:
		for _, item := range v {
			collectContent(item, out)
		}
	case map[string]any:
		typ := strings.ToLower(stringValue(v["type"]))
		if typ == "image" || typ == "input_image" || typ == "image_url" {
			if img, ok := decodeImage(v); ok {
				out.Images = append(out.Images, img)
			}
			return
		}
		if typ == "tool_use" || typ == "tool_call" || typ == "function_call" {
			collectToolCalls([]any{v}, out)
			return
		}
		if typ == "thinking" || typ == "reasoning" || typ == "redacted_thinking" {
			return
		}
		if text := stringValue(v["text"]); text != "" {
			out.Texts = append(out.Texts, text)
		}
		if text := stringValue(v["display_content"]); text != "" {
			out.Texts = append(out.Texts, text)
		}
		if text := stringValue(v["expanded_display_content"]); text != "" {
			out.Texts = append(out.Texts, text)
		}
		if nested, ok := v["content"]; ok {
			collectContent(nested, out)
		}
		if nested, ok := v["output"]; ok {
			collectContent(nested, out)
		}
		if nested, ok := v["result"]; ok {
			collectContent(nested, out)
		}
		if source, ok := v["source"].(map[string]any); ok {
			if img, ok := decodeImage(source); ok {
				out.Images = append(out.Images, img)
			}
		}
	}
}

func collectImages(value any, out *contentCollector) {
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			if img, ok := decodeImage(item); ok {
				out.Images = append(out.Images, img)
			}
		}
	case map[string]any:
		if img, ok := decodeImage(v); ok {
			out.Images = append(out.Images, img)
		}
	}
}

func collectToolCalls(value any, out *contentCollector) {
	items, ok := value.([]any)
	if !ok {
		if one, ok := value.(map[string]any); ok {
			items = []any{one}
		}
	}
	for _, item := range items {
		call, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := stringValue(call["name"])
		if fn, ok := call["function"].(map[string]any); ok {
			if name == "" {
				name = stringValue(fn["name"])
			}
			if call["arguments"] == nil {
				call["arguments"] = fn["arguments"]
			}
		}
		if name == "" {
			name = stringValue(call["tool_name"])
		}
		if name == "" {
			name = stringValue(call["inference_tool_name"])
		}
		if name == "" {
			name = "tool"
		}
		text := "Tool: " + name
		args := call["arguments"]
		if args == nil {
			args = call["parameters"]
		}
		if args == nil {
			args = call["input"]
		}
		if args != nil {
			if b, err := json.Marshal(args); err == nil && string(b) != "null" {
				text += "\n" + string(b)
			}
		}
		out.Texts = append(out.Texts, text)
	}
}

func decodeImage(value any) (imageRef, bool) {
	obj, ok := value.(map[string]any)
	if !ok {
		return imageRef{}, false
	}
	mime := stringValue(obj["mime_type"])
	if mime == "" {
		mime = stringValue(obj["mimeType"])
	}
	if mime == "" {
		mime = stringValue(obj["mime"])
	}
	if mime == "" {
		if types, ok := obj["mime_types"].([]any); ok && len(types) > 0 {
			mime = stringValue(types[0])
		}
	}
	data := stringValue(obj["base64_data"])
	if data == "" {
		data = stringValue(obj["base64Data"])
	}
	if data == "" {
		data = stringValue(obj["data"])
	}
	if strings.HasPrefix(data, "data:") {
		if header, body, ok := strings.Cut(data, ","); ok {
			if strings.HasPrefix(header, "data:") && strings.Contains(header, ";base64") {
				if mime == "" {
					mime = strings.TrimPrefix(strings.TrimSuffix(header, ";base64"), "data:")
				}
				data = body
			}
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err == nil && len(decoded) > 0 {
		if mime == "" {
			mime = "application/octet-stream"
		}
		return imageRef{Mime: mime, Data: decoded}, true
	}
	path := stringValue(obj["source_path"])
	if path == "" {
		path = stringValue(obj["sourcePath"])
	}
	if path != "" {
		return imageRef{Mime: mime, Path: path}, true
	}
	return imageRef{}, false
}

func stringValue(value any) string {
	s, _ := value.(string)
	return s
}

func appendIfMissing(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func failureValue(value any) bool {
	var failed bool
	var walk func(any)
	walk = func(v any) {
		if failed {
			return
		}
		switch x := v.(type) {
		case map[string]any:
			for key, child := range x {
				lower := strings.ToLower(key)
				if lower == "error" || lower == "failure_reason" || lower == "failurereason" || lower == "error_message" {
					if text := stringValue(child); text != "" {
						failed = true
						return
					}
					if childMap, ok := child.(map[string]any); ok && len(childMap) > 0 {
						failed = true
						return
					}
				}
				if lower == "status" || lower == "finish_reason" || lower == "finishreason" {
					status := strings.ToLower(stringValue(child))
					if status == "error" || status == "failed" || status == "failure" || status == "cancelled" {
						failed = true
						return
					}
				}
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	walk(value)
	return failed
}

func contextUsage(payload, metadata any) *model.ContextUsage {
	for _, value := range []any{payload, metadata} {
		if usage := findContextUsage(value); usage != nil {
			return usage
		}
	}
	return nil
}

func findContextUsage(value any) *model.ContextUsage {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			lower := strings.ToLower(key)
			if lower == "context_usage" || lower == "contextusage" || lower == "context" {
				if obj, ok := child.(map[string]any); ok {
					used := numberValue(obj, "used_tokens", "usedTokens", "input_tokens", "inputTokens")
					window := numberValue(obj, "window_tokens", "windowTokens", "max_tokens", "maxTokens")
					if u := model.NewContextUsage(used, window); u != nil {
						return u
					}
				}
			}
			if usage := findContextUsage(child); usage != nil {
				return usage
			}
		}
	case []any:
		for _, child := range v {
			if usage := findContextUsage(child); usage != nil {
				return usage
			}
		}
	}
	return nil
}

func numberValue(obj map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if value, ok := obj[key]; ok {
			switch n := value.(type) {
			case float64:
				return int64(n)
			case json.Number:
				v, _ := n.Int64()
				return v
			case int64:
				return n
			}
		}
	}
	return 0
}
