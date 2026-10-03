package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// Anonymized thread/read items from a real queued follow-up and its reply.
func Test非同期質問の回答をタグではなく回答済みカードで表示する(t *testing.T) {
	th, _ := loadThread(t, "async_reply.json")
	msgs := ConvertThread(th, convertOptions{})
	assertGolden(t, "async_reply.golden.json", msgs)
	ia := msgs[1].Blocks[0].Interaction
	if ia == nil || ia.State != model.InteractionAnswered || ia.Answer != "このプロジェクトのリポジトリ" || len(ia.Questions[0].Options) != 2 {
		t.Fatalf("reply = %+v", ia)
	}
	if latestAsyncQuestion(th) != nil {
		t.Fatal("answered question is still pending")
	}
	// Replies can arrive in a later turn, and that must not change ordering.
	reply := th.Turns[0].Items[1]
	th.Turns[0].Items = th.Turns[0].Items[:1]
	th.Turns = append(th.Turns, Turn{ID: "turn-2", Items: []json.RawMessage{reply}})
	if latestAsyncQuestion(th) != nil || ConvertThread(th, convertOptions{})[1].Blocks[0].Interaction == nil {
		t.Fatal("cross-turn reply was lost")
	}
}

func Test非同期回答の不正形式や引用は元のテキストを保持する(t *testing.T) {
	th, _ := loadThread(t, "async_reply.json")
	var reply item
	json.Unmarshal(th.Turns[0].Items[1], &reply)
	text := reply.Content[0].Text
	for _, invalid := range []string{
		"Example:\n" + text, text + "\nAdditional text", "<send_user_message_question_reply>[]</send_user_message_question_reply>",
		strings.Replace(text, "request_user_input_async", "unknown_tool", 1),
		strings.Replace(text, ",0]", ",-1]", 1),
		strings.Replace(text, ",0]", ",null]", 1),
		strings.Replace(text, "[{", "[null,{", 1),
	} {
		reply.Content[0].Text = invalid
		raw, _ := json.Marshal(reply)
		msgs := ConvertThread(&Thread{Turns: []Turn{{Items: []json.RawMessage{raw}}}}, convertOptions{})
		if len(msgs) != 1 || msgs[0].Blocks[0].Text != invalid {
			t.Fatalf("text was lost: %q", invalid)
		}
	}
}

func Test非同期回答は複数回答と他の入力を保持する(t *testing.T) {
	th, _ := loadThread(t, "async_reply.json")
	var it item
	json.Unmarshal(th.Turns[0].Items[1], &it)
	replies := parseAsyncReplies(it.Content[0].Text)
	replies = append(replies, asyncReply{Answer: "second", Question: "Another?", QuestionItemID: `["request_user_input_async","call_other",1]`})
	data, _ := json.Marshal(replies)
	it.Content[0].Text = "<send_user_message_question_reply>" + string(data) + "</send_user_message_question_reply>"
	it.Content = append(it.Content, userInput{Type: "text", Text: "Extra"}, userInput{Type: "image", URL: "https://example.com/image.png"})
	raw, _ := json.Marshal(it)
	th.Turns[0].Items[1] = raw
	blocks := ConvertThread(th, convertOptions{})[1].Blocks
	if len(blocks) != 4 || blocks[1].Interaction.Answer != "second" || blocks[2].Text != "Extra" || blocks[3].Type != model.BlockImage {
		t.Fatalf("blocks = %+v", blocks)
	}
}

func Test回答済みの質問だけを待機対象から除外する(t *testing.T) {
	th, _ := loadThread(t, "async_reply.json")
	var old item
	json.Unmarshal(th.Turns[0].Items[0], &old)
	old.ID = "call_older"
	raw, _ := json.Marshal(old)
	th.Turns[0].Items = append([]json.RawMessage{raw}, th.Turns[0].Items...)
	if ia := latestAsyncQuestion(th); ia == nil || ia.ID != asyncInputPrefix+old.ID {
		t.Fatalf("older unanswered question = %+v", ia)
	}
	// A reply to index zero must not mark a whole multi-question call answered.
	var multi item
	json.Unmarshal(th.Turns[0].Items[1], &multi)
	multi.Questions = append(multi.Questions, asyncQuestion{Title: "Second?"})
	th.Turns[0].Items[1], _ = json.Marshal(multi)
	if latestAsyncQuestion(th) != nil {
		t.Fatal("partially answered multi-question call fell through to an older question")
	}
}
