package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// Thread items do not keep requestUserInput prompts once they are answered,
// so the app would lose what was asked and chosen. The rollout records them as
// a request_user_input call and its output; answeredInputs turns those back
// into answered cards, placed before the agent message that followed the
// answer (or at the end of the turn).

const answeredInputPrefix = "codex-answered:"

type answeredInput struct {
	Turn        string
	Before      string // thread item id the card goes in front of; "" = end of turn
	Interaction *model.Interaction
}

type rolloutRecord struct {
	Type    string `json:"type"`
	Payload struct {
		Type      string `json:"type"`
		TurnID    string `json:"turn_id"`
		ID        string `json:"id"`
		Role      string `json:"role"`
		Name      string `json:"name"`
		CallID    string `json:"call_id"`
		Arguments string `json:"arguments"`
		Output    string `json:"output"`
	} `json:"payload"`
}

type userInputArgs struct {
	Questions []struct {
		ID       string `json:"id"`
		Header   string `json:"header"`
		Question string `json:"question"`
		Options  []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"questions"`
}

type userInputOutput struct {
	Answers map[string]struct {
		Answers []string `json:"answers"`
	} `json:"answers"`
}

var answeredCache = struct {
	sync.Mutex
	entries map[string]*answeredCacheEntry
}{entries: map[string]*answeredCacheEntry{}}

type answeredCacheEntry struct {
	mu     sync.Mutex
	info   os.FileInfo
	offset int64 // bytes through the last complete record
	parser answeredParser
}

type answeredParser struct {
	out   []answeredInput
	turn  string
	calls map[string]userInputArgs
	open  []int // answers awaiting the next agent message
}

// answeredInputs consumes only appended records. A changed file identity,
// truncation or rewrite resets the parser; an incomplete last line is retried.
func answeredInputs(path string) []answeredInput {
	if path == "" {
		return nil
	}
	answeredCache.Lock()
	e := answeredCache.entries[path]
	if e == nil {
		e = &answeredCacheEntry{}
		answeredCache.entries[path] = e
	}
	answeredCache.Unlock()
	e.mu.Lock()
	defer e.mu.Unlock()
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil
	}
	if e.info != nil && os.SameFile(e.info, st) && e.info.Size() == st.Size() && e.info.ModTime().Equal(st.ModTime()) {
		return append([]answeredInput(nil), e.parser.out...)
	}
	if e.info == nil || !os.SameFile(e.info, st) || st.Size() < e.info.Size() || (st.Size() == e.info.Size() && !st.ModTime().Equal(e.info.ModTime())) {
		e.offset, e.parser = 0, answeredParser{}
	}
	r := bufio.NewReaderSize(io.NewSectionReader(f, e.offset, st.Size()-e.offset), 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			e.parser.consume(line)
			e.offset += int64(len(line))
		}
		if err != nil {
			break
		}
	}
	e.info = st
	// Before can change on the next append. Do not mutate a caller's slice.
	return append([]answeredInput(nil), e.parser.out...)
}

func readAnsweredInputs(path string) []answeredInput {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var p answeredParser
	r := bufio.NewReaderSize(f, 64<<10)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			p.consume(line)
		}
		if err != nil {
			break
		}
	}
	return p.out
}

func (r *answeredParser) consume(line []byte) {
	if !relevantRecord(line, len(r.calls) > 0, len(r.open) > 0) {
		return
	}
	var rec rolloutRecord
	if json.Unmarshal(line, &rec) != nil {
		return
	}
	p := rec.Payload
	switch {
	case rec.Type == "turn_context":
		r.turn, r.open = p.TurnID, nil
	case p.Type == "function_call" && p.Name == "request_user_input":
		var args userInputArgs
		if json.Unmarshal([]byte(p.Arguments), &args) == nil && len(args.Questions) > 0 {
			if r.calls == nil {
				r.calls = map[string]userInputArgs{}
			}
			r.calls[p.CallID] = args
		}
	case p.Type == "function_call_output":
		if args, ok := r.calls[p.CallID]; ok {
			delete(r.calls, p.CallID)
			r.out = append(r.out, answeredInput{Turn: r.turn, Interaction: answeredInteraction(p.CallID, args, p.Output)})
			r.open = append(r.open, len(r.out)-1)
		}
	case p.Type == "message" && p.Role == "assistant" && p.ID != "":
		for _, i := range r.open {
			r.out[i].Before = p.ID
		}
		r.open = nil
	}
}

// relevantRecord skips decoding the bulk of a rollout: only turn starts,
// user input calls, their outputs and (while an answer waits for its place)
// assistant messages matter.
func relevantRecord(line []byte, callsOpen, answerOpen bool) bool {
	switch {
	case bytes.Contains(line, []byte(`"type":"turn_context"`)):
		return true
	case bytes.Contains(line, []byte(`"request_user_input"`)):
		return true
	case callsOpen && bytes.Contains(line, []byte(`"function_call_output"`)):
		return true
	case answerOpen && bytes.Contains(line, []byte(`"role":"assistant"`)):
		return true
	}
	return false
}

func answeredInteraction(callID string, args userInputArgs, output string) *model.Interaction {
	ia := &model.Interaction{
		ID:        answeredInputPrefix + callID,
		Type:      model.InteractionQuestions,
		State:     model.InteractionAnswered,
		Title:     "Codex needs input",
		Supported: true,
	}
	var res userInputOutput
	json.Unmarshal([]byte(output), &res)
	var answers []string
	for _, q := range args.Questions {
		mq := model.Question{ID: q.ID, Type: model.QuestionSelect, Header: q.Header, Question: q.Question}
		for _, o := range q.Options {
			mq.Options = append(mq.Options, model.Option{Label: o.Label, Description: o.Description})
		}
		if len(mq.Options) == 0 {
			mq.Type = model.QuestionText
		}
		ia.Questions = append(ia.Questions, mq)
		a := strings.Join(res.Answers[q.ID].Answers, ", ")
		if a == "" {
			continue
		}
		if len(args.Questions) > 1 {
			a = q.Question + " → " + a
		}
		answers = append(answers, a)
	}
	ia.Answer = model.Truncate(strings.Join(answers, "\n"), 500)
	if ia.Answer == "" {
		ia.Answer = "Not answered"
	}
	return ia
}

// withAnswered places answered prompts among a turn's converted items.
func withAnswered(turnID string, ts time.Time, msgs []model.Message, answered []answeredInput) []model.Message {
	var out []model.Message
	placed := map[int]bool{}
	card := func(a answeredInput) model.Message {
		return model.Message{ID: a.Interaction.ID, Role: model.RoleAssistant, Timestamp: ts,
			Blocks: []model.Block{{Type: model.BlockInteraction, Interaction: a.Interaction}}}
	}
	for _, m := range msgs {
		for i, a := range answered {
			if a.Turn == turnID && a.Before != "" && a.Before == m.ID && !placed[i] {
				out = append(out, card(a))
				placed[i] = true
			}
		}
		out = append(out, m)
	}
	for i, a := range answered {
		if a.Turn == turnID && !placed[i] {
			out = append(out, card(a))
		}
	}
	return out
}
