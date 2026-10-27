package codex

import (
	"encoding/json"
	"encoding/xml"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// delegationMessage turns a message another Codex thread sent with
// send_message_to_thread into a session message. Codex delivers it as the
// output of a function call this thread never made, so shown as a tool block
// it would be folded into the agent's work.
func delegationMessage(it *item, ts time.Time, sessionID string) (model.Message, bool) {
	if it.Name != "send_message_to_thread" {
		return model.Message{}, false
	}
	var output string
	if json.Unmarshal(it.Output, &output) != nil {
		return model.Message{}, false
	}
	output = strings.TrimSpace(output)
	if !strings.HasPrefix(output, "<codex_delegation>") {
		return model.Message{}, false
	}
	var frame struct {
		Source string `xml:"source_thread_id"`
		Input  string `xml:"input"`
	}
	if xml.Unmarshal([]byte(output), &frame) != nil || strings.TrimSpace(frame.Input) == "" {
		return model.Message{}, false
	}
	block := model.Block{Type: model.BlockSessionMessage, Text: strings.TrimSpace(frame.Input)}
	if source := strings.TrimSpace(frame.Source); threadIDPattern.MatchString(source) && gatewayID(source) != sessionID {
		block.SessionID = gatewayID(source)
	}
	return model.Message{ID: it.ID, Role: model.RoleSystem, Timestamp: ts, Blocks: []model.Block{block}}, true
}
