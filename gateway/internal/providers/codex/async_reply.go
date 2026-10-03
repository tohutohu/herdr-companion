package codex

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

type asyncReply struct {
	Answer         string `json:"answer"`
	Question       string `json:"question"`
	QuestionItemID string `json:"questionItemId"`
	callID         string
	index          int
}

// Only unwrap a complete, valid protocol message. Quoted examples and unknown
// formats remain visible as ordinary text, so no user content is lost.
func parseAsyncReplies(text string) []asyncReply {
	const start, end = "<send_user_message_question_reply>", "</send_user_message_question_reply>"
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, start) || !strings.HasSuffix(text, end) {
		return nil
	}
	var replies []asyncReply
	if json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(text, start), end)), &replies) != nil || len(replies) == 0 {
		return nil
	}
	for i := range replies {
		r := &replies[i]
		var id []json.RawMessage
		var tool string
		if json.Unmarshal([]byte(r.QuestionItemID), &id) != nil || len(id) != 3 ||
			json.Unmarshal(id[0], &tool) != nil || tool != "request_user_input_async" ||
			json.Unmarshal(id[1], &r.callID) != nil || r.callID == "" ||
			json.Unmarshal(id[2], &r.index) != nil || r.index < 0 || string(id[2]) == "null" ||
			strings.TrimSpace(r.Question) == "" || strings.TrimSpace(r.Answer) == "" {
			return nil
		}
	}
	return replies
}

func asyncReplyBlock(reply asyncReply, id string, questions map[string][]asyncQuestion) model.Block {
	q := model.Question{ID: strconv.Itoa(reply.index), Type: model.QuestionText, Question: reply.Question}
	if qs := questions[reply.callID]; reply.index < len(qs) && qs[reply.index].Title == reply.Question {
		for _, label := range qs[reply.index].Options {
			q.Options = append(q.Options, model.Option{Label: label})
		}
		if len(q.Options) > 0 {
			q.Type = model.QuestionSelect
		}
	}
	return model.Block{Type: model.BlockInteraction, Interaction: &model.Interaction{
		ID: id, Type: model.InteractionQuestions, State: model.InteractionAnswered,
		Title: "Codex needs input", Supported: true, Questions: []model.Question{q}, Answer: reply.Answer,
	}}
}
