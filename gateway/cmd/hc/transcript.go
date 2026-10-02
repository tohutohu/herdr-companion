package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

// Tool calls arrive as assistant text rendered by the Gateway; these are
// the shapes the Android app folds too (ToolSummary.kt).
var toolCallPrefixes = []string{
	"Read ", "Edit ", "MultiEdit ", "Write ", "NotebookEdit ", "Glob ", "Grep ",
	"WebFetch ", "WebSearch ", "Agent: ", "▸ ",
}

func isToolCallText(text string) bool {
	lines := strings.Split(text, "\n")
	if strings.HasPrefix(lines[0], "▸ ") || strings.HasPrefix(lines[0], "$ ") {
		return true
	}
	if len(lines) > 1 && strings.HasPrefix(lines[1], "$ ") {
		return true
	}
	if len(lines) != 1 {
		return false
	}
	for _, p := range toolCallPrefixes {
		if strings.HasPrefix(lines[0], p) {
			return true
		}
	}
	return false
}

func isActivity(role model.Role, b model.Block) bool {
	switch {
	case b.Type == model.BlockInteraction:
		return false
	case role == model.RoleTool:
		return true
	case role == model.RoleAssistant:
		return b.Type == model.BlockText && isToolCallText(b.Text)
	}
	return false
}

// row is one item of the folded transcript: a message's prose, or a run of
// tool calls and outputs between two pieces of prose.
type row struct {
	msg   model.Message // prose: the message with only its prose blocks
	tools []toolPart    // activity rows only
}

type toolPart struct {
	call bool
	text string
}

func (r row) isTools() bool { return r.tools != nil }

// foldRows splits messages into rows the way the app shows them: prose
// stays, tool calls and outputs between prose collapse into one row.
func foldRows(msgs []model.Message) []row {
	var rows []row
	var group *row
	flush := func() {
		if group != nil {
			rows = append(rows, *group)
			group = nil
		}
	}
	for _, m := range msgs {
		var prose []model.Block
		lastActivity := false
		emitProse := func() {
			if len(prose) > 0 {
				p := m
				p.Blocks = prose
				rows = append(rows, row{msg: p})
				prose = nil
			}
		}
		for _, b := range m.Blocks {
			activity := isActivity(m.Role, b)
			if b.Type == model.BlockFile {
				// A file goes with what came before it: the file a call read,
				// or one the prose mentions.
				activity = lastActivity
			}
			lastActivity = activity
			if activity {
				emitProse()
				if group == nil {
					group = &row{tools: []toolPart{}}
				}
				switch b.Type {
				case model.BlockText:
					group.tools = append(group.tools, toolPart{call: m.Role == model.RoleAssistant, text: b.Text})
				case model.BlockFile:
					group.tools = append(group.tools, toolPart{text: "[file " + fileRef(b) + "]"})
				}
				continue
			}
			flush()
			prose = append(prose, b)
		}
		emitProse()
	}
	flush()
	return rows
}

type renderOpts struct {
	short    string // session id as the reader typed it, for hints
	maxChars int    // per text block; 0 = unlimited
	tools    bool   // list every tool call instead of one summary line
}

func renderRow(w io.Writer, r row, o renderOpts) {
	if r.isTools() {
		renderTools(w, r.tools, o)
		return
	}
	m := r.msg
	label := string(m.Role)
	if m.Queued {
		label += " (queued, not yet read by the agent)"
	}
	fmt.Fprintf(w, "── %s %s · %s\n", label, clock(m.Timestamp), shortMsgID(m.ID))
	for i, b := range m.Blocks {
		switch b.Type {
		case model.BlockInteraction:
			if b.Interaction != nil {
				renderInteraction(w, *b.Interaction, o.short)
			}
		case model.BlockImage:
			fmt.Fprintf(w, "[image %d]\n", i)
		case model.BlockFile:
			fmt.Fprintf(w, "[file %s]\n", fileRef(b))
		default:
			fmt.Fprintln(w, cut(strings.TrimRight(b.Text, "\n"), o.maxChars, "hc read "+o.short+" --msg "+shortMsgID(m.ID)))
		}
	}
}

func renderTools(w io.Writer, parts []toolPart, o renderOpts) {
	calls := 0
	latest := ""
	for _, p := range parts {
		if p.call {
			calls++
			latest = firstLine(p.text)
		}
	}
	if latest == "" && len(parts) > 0 {
		latest = firstLine(parts[len(parts)-1].text)
	}
	if !o.tools {
		fmt.Fprintf(w, "   ⋯ %d tool call%s, latest: %s\n", calls, plural(calls), clip(latest, 160))
		return
	}
	for _, p := range parts {
		lines := strings.Count(strings.TrimRight(p.text, "\n"), "\n") + 1
		if p.call {
			fmt.Fprintf(w, "   > %s\n", clip(oneLine(p.text), 200))
		} else if lines > 1 {
			fmt.Fprintf(w, "     %s  (%d lines)\n", clip(firstLine(p.text), 160), lines)
		} else {
			fmt.Fprintf(w, "     %s\n", clip(firstLine(p.text), 160))
		}
	}
}

// renderInteraction prints a question or approval with the exact command
// that answers it while it is pending.
func renderInteraction(w io.Writer, ia model.Interaction, short string) {
	kind := "question"
	if ia.Awaits() == model.InteractionApproval {
		kind = "approval"
	}
	if ia.Kind == model.KindPlan {
		kind = "plan approval"
	}
	title := ia.Title
	if ia.State != model.InteractionPending {
		line := fmt.Sprintf("[%s %s]", kind, ia.State)
		if title != "" {
			line += " " + clip(oneLine(title), 120)
		}
		if ia.Answer != "" {
			line += " → " + clip(oneLine(ia.Answer), 200)
		}
		fmt.Fprintln(w, line)
		return
	}
	fmt.Fprintf(w, "[%s PENDING] %s\n", kind, clip(oneLine(title), 200))
	if ia.Detail != "" {
		fmt.Fprintln(w, indent(cut(ia.Detail, 1500, "hc show "+short+" --full"), "  "))
	}
	for _, q := range ia.Questions {
		head := q.Question
		if q.Header != "" && q.Header != q.Question {
			head = q.Header + ": " + q.Question
		}
		fmt.Fprintf(w, "  %s [%s] %s\n", q.ID, q.Type, head)
		for i, op := range q.Options {
			line := fmt.Sprintf("    %d) %s", i+1, op.Label)
			if op.Description != "" {
				line += " — " + clip(oneLine(op.Description), 200)
			}
			fmt.Fprintln(w, line)
			if op.Preview != "" {
				fmt.Fprintln(w, indent(cut(op.Preview, 400, ""), "       │ "))
			}
		}
		if q.AllowOther || q.Type == model.QuestionText {
			other := "free text"
			if q.OtherLabel != "" {
				other += " (" + q.OtherLabel + ")"
			}
			fmt.Fprintf(w, "    *) %s\n", other)
		}
	}
	if !ia.Supported {
		fmt.Fprintf(w, "  answer: not supported here; use the terminal: hc term %s, then hc keys %s KEY...\n", short, short)
		return
	}
	fmt.Fprintf(w, "  answer: %s\n", answerHint(ia, short))
}

func answerHint(ia model.Interaction, short string) string {
	if ia.Type == model.InteractionApproval && len(ia.Questions) == 0 {
		decisions := ia.Decisions
		if len(decisions) == 0 {
			decisions = []string{model.DecisionApprove, model.DecisionDeny}
		}
		return "hc answer " + short + " " + strings.Join(decisions, " | ")
	}
	if len(ia.Questions) == 1 {
		q := ia.Questions[0]
		switch q.Type {
		case model.QuestionMultiSelect:
			return "hc answer " + short + " 1,3   (option numbers or labels; or free text)"
		case model.QuestionText:
			return "hc answer " + short + " \"your text\""
		}
		return "hc answer " + short + " 1   (option number or label; or free text)"
	}
	var ex []string
	for _, q := range ia.Questions {
		ex = append(ex, q.ID+"=1")
	}
	return "hc answer " + short + " " + strings.Join(ex, " ") + "   (QID=number|label|text per question)"
}

// pendingInteractions returns the interactions still waiting for an answer.
func pendingInteractions(msgs []model.Message) []model.Interaction {
	var out []model.Interaction
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == model.BlockInteraction && b.Interaction != nil && b.Interaction.State == model.InteractionPending {
				out = append(out, *b.Interaction)
			}
		}
	}
	return out
}

// newSince returns where the messages the reader has not seen begin. A
// last-seen message that changed since (a reply still being written, a
// question that got answered) is shown again.
func newSince(msgs []model.Message, mark *readMark) int {
	if mark == nil {
		return 0
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].ID == mark.LastID {
			if messageHash(msgs[i]) != mark.Hash {
				return i
			}
			return i + 1
		}
	}
	// The mark is gone (a queued prompt replaced by the real entry): fall
	// back to time.
	for i, m := range msgs {
		if m.Timestamp.After(mark.At) {
			return i
		}
	}
	return len(msgs)
}

// markAt is the read mark after showing msgs. Queued prompts are not
// marked: they are replaced once the agent takes them.
func markAt(msgs []model.Message) (readMark, bool) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if !msgs[i].Queued {
			return readMark{LastID: msgs[i].ID, Hash: messageHash(msgs[i]), At: msgs[i].Timestamp}, true
		}
	}
	return readMark{}, false
}

func messageHash(m model.Message) string {
	b, _ := json.Marshal(m)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func findMessage(msgs []model.Message, prefix string) (model.Message, error) {
	var found []model.Message
	for _, m := range msgs {
		if m.ID == prefix {
			return m, nil
		}
		if strings.HasPrefix(m.ID, prefix) {
			found = append(found, m)
		}
	}
	switch len(found) {
	case 0:
		return model.Message{}, fmt.Errorf("no message %q in this session", prefix)
	case 1:
		return found[0], nil
	}
	return model.Message{}, fmt.Errorf("message prefix %q is ambiguous (%d matches)", prefix, len(found))
}

// writeMarkdown writes the whole transcript, nothing folded or cut.
func writeMarkdown(w io.Writer, s model.Session, msgs []model.Message) {
	fmt.Fprintf(w, "# %s\n\n", firstNonEmpty(s.Title, s.Project, s.ID))
	fmt.Fprintf(w, "- id: %s\n- status: %s\n- project: %s\n- cwd: %s\n- updated: %s\n\n",
		s.ID, s.Status, s.Project, s.Cwd, s.UpdatedAt.Local().Format(time.RFC3339))
	for _, m := range msgs {
		fmt.Fprintf(w, "## %s · %s · %s\n\n", m.Role, m.Timestamp.Local().Format("2006-01-02 15:04:05"), m.ID)
		for i, b := range m.Blocks {
			switch b.Type {
			case model.BlockInteraction:
				if b.Interaction != nil {
					fmt.Fprintln(w, "```")
					renderInteraction(w, *b.Interaction, s.ID)
					fmt.Fprintln(w, "```")
				}
			case model.BlockImage:
				fmt.Fprintf(w, "[image %d]\n", i)
			case model.BlockFile:
				fmt.Fprintf(w, "[file %s]\n", fileRef(b))
			default:
				if isActivity(m.Role, b) {
					fmt.Fprintf(w, "```\n%s\n```\n", strings.TrimRight(b.Text, "\n"))
				} else {
					fmt.Fprintln(w, strings.TrimRight(b.Text, "\n"))
				}
			}
			fmt.Fprintln(w)
		}
	}
}

func fileRef(b model.Block) string {
	if b.Line > 0 {
		return fmt.Sprintf("%s:%d", b.Path, b.Line)
	}
	return b.Path
}

// cut shortens text to max runes and says how to get the rest.
func cut(text string, max int, more string) string {
	if max <= 0 || utf8.RuneCountInString(text) <= max {
		return text
	}
	r := []rune(text)
	note := fmt.Sprintf("… (+%d chars", len(r)-max)
	if more != "" {
		note += "; full: " + more
	}
	return string(r[:max]) + note + ")"
}

func clip(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max-1]) + "…"
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

// oneLine joins a tool call's description and command ("List files\n$ ls").
func oneLine(s string) string {
	var parts []string
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ⏎ ")
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func shortMsgID(id string) string { return clipID(id, 8) }

func clipID(id string, n int) string {
	if len(id) <= n {
		return id
	}
	return id[:n]
}

func clock(t time.Time) string {
	if t.IsZero() {
		return "--:--"
	}
	lt := t.Local()
	if d := time.Since(t); d > 20*time.Hour || d < -20*time.Hour {
		return lt.Format("01-02 15:04")
	}
	return lt.Format("15:04")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
