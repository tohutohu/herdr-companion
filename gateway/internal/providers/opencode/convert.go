package opencode

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

type modelRef struct {
	ID         string `json:"id"`
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
	Variant    string `json:"variant"`
}

func (m modelRef) name() string {
	id := m.ID
	if id == "" {
		id = m.ModelID
	}
	if id != "" && m.ProviderID != "" {
		return m.ProviderID + "/" + id
	}
	return id
}

type attachment struct {
	Data   string `json:"data"`
	Source struct {
		Type string `json:"type"`
		URI  string `json:"uri"`
	} `json:"source"`
	Type     string `json:"type"`
	URL      string `json:"url"`
	URI      string `json:"uri"`
	Mime     string `json:"mime"`
	Filename string `json:"filename"`
	Name     string `json:"name"`
}
type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Tool string `json:"tool"`
	Name string `json:"name"`
	attachment
	State struct {
		Status  string          `json:"status"`
		Input   json.RawMessage `json:"input"`
		Title   string          `json:"title"`
		Output  string          `json:"output"`
		Error   json.RawMessage `json:"error"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			attachment
		} `json:"content"`
		Attachments []attachment `json:"attachments"`
	} `json:"state"`
}
type event struct {
	Outcome    string            `json:"outcome"`
	Role       string            `json:"role"`
	Type       string            `json:"type"`
	Text       string            `json:"text"`
	Summary    string            `json:"summary"`
	Command    string            `json:"command"`
	Output     json.RawMessage   `json:"output"`
	Model      modelRef          `json:"model"`
	ModelID    string            `json:"modelID"`
	ProviderID string            `json:"providerID"`
	Agent      string            `json:"agent"`
	Variant    string            `json:"variant"`
	Cost       *float64          `json:"cost"`
	Error      json.RawMessage   `json:"error"`
	Files      []attachment      `json:"files"`
	Content    []json.RawMessage `json:"content"`
}
type inlineImage struct {
	Mime string
	Data []byte
}
type conversion struct {
	Messages            []model.Message
	Images              map[string][]inlineImage
	Model, Effort, Mode string
	Cost                *model.Cost
	Failed              bool
}

func (p *Provider) convert(id string, rs []record) conversion {
	out := conversion{Messages: []model.Message{}, Images: map[string][]inlineImage{}}
	sid := p.Name() + ":" + id
	for _, r := range rs {
		msg := model.Message{ID: r.ID, Timestamp: time.UnixMilli(r.Created).UTC(), Role: model.RoleSystem, Blocks: []model.Block{}}
		var e event
		if err := json.Unmarshal(r.Raw, &e); err != nil {
			msg.Blocks = append(msg.Blocks, p.unsupported(sid, "invalid message", r, deadletter.ParseError))
			out.Messages = append(out.Messages, msg)
			continue
		}
		kind := r.Kind
		if kind == "" {
			kind = e.Role
		}
		if kind == "" {
			kind = e.Type
		}
		switch kind {
		case "user":
			msg.Role = model.RoleUser
			out.Failed = false
		case "assistant":
			msg.Role = model.RoleAssistant
			out.Failed = len(e.Error) > 0 && string(e.Error) != "null"
			if out.Failed {
				msg.Blocks = append(msg.Blocks, model.TextBlock("Error: "+errorText(e.Error)))
			}
		case "system", "synthetic", "skill":
		case "shell":
			msg.Role = model.RoleTool
			msg.Blocks = append(msg.Blocks, model.TextBlock(e.Command+"\n"+shellOutput(e.Output)))
		case "compaction":
			msg.Blocks = append(msg.Blocks, model.TextBlock("Context summary\n"+e.Summary))
		case "idle":
			out.Failed = e.Outcome == "failed"
		case "model-switched", "agent-switched", "location-switched":
		default:
			msg.Blocks = append(msg.Blocks, p.unsupported(sid, "message type "+kind, r, deadletter.UnknownEvent))
		}
		m := e.Model.name()
		if m == "" {
			m = modelRef{ID: e.ModelID, ProviderID: e.ProviderID}.name()
		}
		if m != "" {
			out.Model = m
			out.Effort = e.Model.Variant
			if out.Effort == "" {
				out.Effort = e.Variant
			}
		}
		if e.Agent != "" {
			out.Mode = e.Agent
		}
		if e.Cost != nil {
			if out.Cost == nil {
				out.Cost = &model.Cost{}
			}
			out.Cost.USD += *e.Cost
		}
		if e.Text != "" {
			msg.Blocks = append(msg.Blocks, model.TextBlock(e.Text))
		}
		for _, a := range e.Files {
			out.addFile(&msg, sid, a)
		}
		parts := e.Content
		if r.Kind == "" {
			parts = r.Parts
		}
		for _, raw := range parts {
			var c content
			if err := json.Unmarshal(raw, &c); err != nil {
				msg.Blocks = append(msg.Blocks, p.unsupported(sid, "invalid content", r, deadletter.ParseError))
				continue
			}
			switch c.Type {
			case "text":
				if c.Text != "" {
					msg.Blocks = append(msg.Blocks, model.TextBlock(c.Text))
				}
			case "file":
				out.addFile(&msg, sid, c.attachment)
			case "tool":
				name := c.Tool
				if name == "" {
					name = c.Name
				}
				detail := c.State.Title
				if detail == "" && len(c.State.Input) > 0 {
					detail = providers.OneLine(string(c.State.Input), 1000)
				}
				text := name
				if detail != "" {
					text += "\n" + detail
				}
				if c.State.Output != "" {
					text += "\n" + c.State.Output
				}
				for _, v := range c.State.Content {
					if v.Type == "text" {
						text += "\n" + v.Text
					} else if v.Type == "file" {
						out.addFile(&msg, sid, v.attachment)
					}
				}
				if len(c.State.Error) > 0 && string(c.State.Error) != "null" {
					text += "\n" + errorText(c.State.Error)
				}
				msg.Blocks = append(msg.Blocks, model.TextBlock(text))
				for _, a := range c.State.Attachments {
					out.addFile(&msg, sid, a)
				}
			case "reasoning", "step-start", "step-finish", "snapshot", "patch", "agent", "retry": // provider bookkeeping and hidden reasoning
			case "compaction":
				msg.Blocks = append(msg.Blocks, model.TextBlock("Context compacted"))
			case "subtask":
				msg.Blocks = append(msg.Blocks, model.TextBlock("Subagent task"))
			default:
				msg.Blocks = append(msg.Blocks, p.unsupported(sid, "content type "+c.Type, r, deadletter.UnknownContent))
			}
		}
		if len(msg.Blocks) > 0 {
			out.Messages = append(out.Messages, msg)
		}
	}
	return out
}

func errorText(raw []byte) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var e struct {
		Message string `json:"message"`
		Data    struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &e) == nil {
		if e.Message != "" {
			return e.Message
		}
		if e.Data.Message != "" {
			return e.Data.Message
		}
	}
	return string(raw)
}

func (c *conversion) addFile(msg *model.Message, sid string, a attachment) {
	uri := a.URI
	if a.Data != "" {
		uri = "data:" + a.Mime + ";base64," + a.Data
	} else if a.Source.URI != "" {
		uri = a.Source.URI
	}
	if uri == "" {
		uri = a.URL
	}
	name := a.Name
	if name == "" {
		name = a.Filename
	}
	if strings.HasPrefix(uri, "data:") && strings.HasPrefix(a.Mime, "image/") {
		header, body, ok := strings.Cut(uri, ",")
		if ok && strings.HasSuffix(header, ";base64") && len(body) <= 20<<20 {
			if data, err := base64.StdEncoding.DecodeString(body); err == nil {
				index := len(c.Images[msg.ID])
				c.Images[msg.ID] = append(c.Images[msg.ID], inlineImage{a.Mime, data})
				msg.Blocks = append(msg.Blocks, model.Block{Type: model.BlockImage, URL: providers.ImageURL(sid, msg.ID, index)})
				return
			}
		}
	}
	if u, err := url.Parse(uri); err == nil && u.Scheme == "file" && (u.Host == "" || u.Host == "localhost") {
		if strings.HasPrefix(a.Mime, "image/") {
			msg.Blocks = append(msg.Blocks, model.Block{Type: model.BlockImage, URL: providers.FileImageURL(sid, u.Path)})
		} else {
			msg.Blocks = append(msg.Blocks, model.Block{Type: model.BlockFile, Path: u.Path, Text: name})
		}
		return
	}
	if name == "" {
		name = "Attachment"
	}
	msg.Blocks = append(msg.Blocks, model.TextBlock(name))
}
func (p *Provider) unsupported(sid, description string, r record, kind deadletter.Kind) model.Block {
	parts := make([]string, len(r.Parts))
	for i, part := range r.Parts {
		parts[i] = string(part)
	}
	p.sink.Record(p.Name(), sid, kind, description, replayRecord{ID: r.ID, Kind: r.Kind, Created: r.Created, Raw: string(r.Raw), Parts: parts})
	return model.TextBlock("Unsupported event: " + description)
}

type replayRecord struct {
	ID, Kind string
	Created  int64
	Raw      string
	Parts    []string
}

// Replay accepts the complete source record saved with every parse failure.
func Replay(raw []byte) ([]model.Message, []deadletter.Entry) {
	sink := &deadletter.Recorder{}
	p := New(Config{}, nil, sink)
	var saved replayRecord
	if err := json.Unmarshal(raw, &saved); err != nil {
		sink.Record(p.Name(), "", deadletter.ParseError, err.Error(), json.RawMessage(raw))
		return nil, sink.Entries
	}
	r := record{ID: saved.ID, Kind: saved.Kind, Created: saved.Created, Raw: json.RawMessage(saved.Raw)}
	for _, part := range saved.Parts {
		r.Parts = append(r.Parts, json.RawMessage(part))
	}
	c := p.convert("replay", []record{r})
	return c.Messages, sink.Entries
}

func questionID(index int) string { return strconv.Itoa(index) }
func invalidAnswer() error        { return fmt.Errorf("invalid OpenCode interaction response") }

func shellOutput(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var page struct {
		Output string `json:"output"`
	}
	json.Unmarshal(raw, &page)
	return page.Output
}
