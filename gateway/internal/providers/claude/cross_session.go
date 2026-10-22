package claude

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

type peerOrigin struct {
	Kind string `json:"kind"`
	From string `json:"from"`
	Name string `json:"name"`
	Body string `json:"body"`
}

// crossSessionMessage accepts the host's peer metadata or a complete frame at
// the start of a meta prompt. Quoted frames in ordinary user prompts stay text.
func crossSessionMessage(e *entry) *model.Message {
	origin := e.Origin
	var raw json.RawMessage
	switch {
	case e.Type == "user" && e.Message != nil:
		raw = e.Message.Content
	case e.Type == "attachment" && e.Attachment != nil:
		origin, raw = e.Attachment.Origin, e.Attachment.Prompt
	case e.Type == "queue-operation":
		raw = e.Content
	default:
		return nil
	}
	var peer *peerOrigin
	if origin != nil && origin.Kind == "peer" && peerSocketRE.MatchString(origin.From) && strings.TrimSpace(origin.Body) != "" {
		peer = origin
	} else if e.IsMeta || (e.Attachment != nil && e.Attachment.IsMeta) || e.Type == "queue-operation" {
		peer = crossSessionFrame(queuedContentText(raw))
	}
	if peer == nil {
		return nil
	}
	return &model.Message{ID: e.UUID, Role: model.RoleSystem, Timestamp: e.Timestamp,
		Blocks: []model.Block{{Type: model.BlockSessionMessage, Text: strings.TrimSpace(peer.Body),
			SenderName: peer.Name, SenderAddress: peer.From}}}
}

func crossSessionFrame(text string) *peerOrigin {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "Another Claude session sent a message:\n")
	if !strings.HasPrefix(text, "<cross-session-message ") {
		return nil
	}
	d := xml.NewDecoder(strings.NewReader(text))
	token, err := d.Token()
	start, ok := token.(xml.StartElement)
	if err != nil || !ok || start.Name.Local != "cross-session-message" {
		return nil
	}
	p := &peerOrigin{Kind: "peer"}
	for _, a := range start.Attr {
		switch a.Name.Local {
		case "from":
			p.From = a.Value
		case "from-name":
			p.Name = a.Value
		}
	}
	end := strings.LastIndex(text, "</cross-session-message>")
	if end < int(d.InputOffset()) || !peerSocketRE.MatchString(p.From) {
		return nil
	}
	p.Body = strings.TrimSpace(text[d.InputOffset():end])
	if p.Body == "" {
		return nil
	}
	return p
}

var peerSocketRE = regexp.MustCompile(`^uds:(/[^\x00\r\n]*/([1-9][0-9]*)\.sock)$`)

// Resolve on each read, outside the transcript cache: a peer can disappear or
// resume while the receiving transcript remains unchanged. Never mutate cached
// blocks. PID/socket reuse must not turn an old message into a link to a new peer.
func (p *Provider) resolveSessionSenders(messages []model.Message) []model.Message {
	type peerSession struct {
		SessionID string `json:"sessionId"`
		Socket    string `json:"messagingSocketPath"`
		Name      string `json:"name"`
		StartedAt int64  `json:"startedAt"`
		NameSince int64  `json:"nameSince"`
	}
	registry := map[string]*peerSession{}
	for i, m := range messages {
		for j, b := range m.Blocks {
			if b.Type != model.BlockSessionMessage {
				continue
			}
			match := peerSocketRE.FindStringSubmatch(b.SenderAddress)
			if match == nil {
				continue
			}
			r, seen := registry[b.SenderAddress]
			if !seen {
				// Read only the PID's public JSON record, never its .key file.
				f, err := os.Open(filepath.Join(p.configDir, "sessions", match[2]+".json"))
				if err == nil {
					var candidate peerSession
					err = json.NewDecoder(io.LimitReader(f, 64*1024)).Decode(&candidate)
					f.Close()
					if err == nil && candidate.Socket == match[1] && sessionIDPattern.MatchString(candidate.SessionID) {
						r = &candidate
					}
				}
				registry[b.SenderAddress] = r
			}
			if r == nil || r.StartedAt <= 0 || m.Timestamp.IsZero() || time.UnixMilli(r.StartedAt).After(m.Timestamp) {
				continue
			}
			// A renamed process cannot reliably identify the earlier sender.
			if b.SenderName != "" && (r.Name != b.SenderName || (r.NameSince > 0 && time.UnixMilli(r.NameSince).After(m.Timestamp))) {
				continue
			}
			if _, err := p.transcriptPath(r.SessionID); err != nil {
				continue
			}
			if gatewayID(r.SessionID) == b.SessionID {
				continue
			}
			messages[i].Blocks = slices.Clone(messages[i].Blocks)
			messages[i].Blocks[j].SessionID = gatewayID(r.SessionID)
		}
	}
	return messages
}
