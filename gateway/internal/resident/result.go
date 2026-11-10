package resident

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// ResultSchema is the JSON Schema every run's final answer must match, for
// all providers (Claude's outputFormat, Codex's outputSchema, and DSH's
// final JSON text validated by ParseResult). It stays
// within what strict structured outputs accept: every property required,
// no free-form objects. Memory values therefore travel as JSON text in
// statePatch[].value.
var ResultSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["schemaVersion", "outcome", "summary", "report", "notification", "statePatch", "memorySummary", "nextWakeAt", "question", "artifacts"],
  "properties": {
    "schemaVersion": {"type": "integer", "enum": [1]},
    "outcome": {"type": "string", "enum": ["changed", "unchanged", "blocked", "failed"]},
    "summary": {"type": "string", "description": "One or two sentences: what changed, or why nothing did."},
    "report": {"type": "string", "description": "The reply shown in the task conversation (Markdown). Empty when there is nothing to add to summary."},
    "notification": {
      "type": "object",
      "additionalProperties": false,
      "required": ["requested", "key"],
      "properties": {
        "requested": {"type": "boolean"},
        "key": {"type": ["string", "null"], "description": "Stable id of the event, the same on every run that sees it (e.g. repo:pr:123:ci:run456:success)."}
      }
    },
    "statePatch": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["op", "key", "value"],
        "properties": {
          "op": {"type": "string", "enum": ["set", "remove"]},
          "key": {"type": "string"},
          "value": {"type": ["string", "null"], "description": "For set: the new value as JSON text, e.g. \"[\\\"run456\\\"]\" or \"\\\"abc\\\"\". Null for remove."}
        }
      }
    },
    "memorySummary": {"type": ["string", "null"], "description": "Replaces the task's memory summary; null keeps it."},
    "nextWakeAt": {"type": ["string", "null"], "description": "RFC 3339 time for one extra check, or null."},
    "question": {
      "anyOf": [
        {"type": "null"},
        {
          "type": "object",
          "additionalProperties": false,
          "required": ["text", "options"],
          "properties": {
            "text": {"type": "string"},
            "options": {"type": "array", "items": {"type": "string"}}
          }
        }
      ]
    },
    "artifacts": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["path", "title"],
        "properties": {
          "path": {"type": "string"},
          "title": {"type": "string"}
        }
      }
    }
  }
}`)

// Result is a run's final answer, decoded.
type Result struct {
	SchemaVersion int    `json:"schemaVersion"`
	Outcome       string `json:"outcome"`
	Summary       string `json:"summary"`
	Report        string `json:"report"`
	Notification  struct {
		Requested bool    `json:"requested"`
		Key       *string `json:"key"`
	} `json:"notification"`
	StatePatch    []PatchOp `json:"statePatch"`
	MemorySummary *string   `json:"memorySummary"`
	NextWakeAt    *string   `json:"nextWakeAt"`
	Question      *struct {
		Text    string   `json:"text"`
		Options []string `json:"options"`
	} `json:"question"`
	Artifacts []struct {
		Path  string `json:"path"`
		Title string `json:"title"`
	} `json:"artifacts"`
}

// PatchOp is one memory state update. Values are JSON text; they are stored
// as JSON and never evaluated.
type PatchOp struct {
	Op    string  `json:"op"`
	Key   string  `json:"key"`
	Value *string `json:"value"`
}

// ErrContract marks a run whose answer did not meet the result contract.
var ErrContract = errors.New("result contract violated")

func contractErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrContract, fmt.Sprintf(format, a...))
}

var stateKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

const (
	maxSummaryRunes = 1000
	maxReportBytes  = 32 << 10
	maxKeyLen       = 200
	maxQuestionLen  = 2000
	maxOptions      = 8
	maxArtifacts    = 20
	maxPatchOps     = 100
)

// ParseResult decodes and checks raw against the contract. It accepts the
// JSON object alone or wrapped in a Markdown code fence (some agents add one
// around their final message).
func ParseResult(raw []byte) (*Result, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, contractErr("the agent returned no result")
	}
	if bytes.HasPrefix(raw, []byte("```")) {
		if i := bytes.IndexByte(raw, '\n'); i > 0 {
			raw = bytes.TrimSpace(bytes.TrimSuffix(bytes.TrimSpace(raw[i+1:]), []byte("```")))
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var r Result
	if err := dec.Decode(&r); err != nil {
		return nil, contractErr("the result is not valid JSON for the schema: %v", err)
	}
	if dec.More() {
		return nil, contractErr("the result has trailing data")
	}
	// Required fields: decode fills zero values, so check presence.
	var present map[string]json.RawMessage
	json.Unmarshal(raw, &present)
	for _, k := range []string{"schemaVersion", "outcome", "summary", "notification", "statePatch", "nextWakeAt", "question", "artifacts"} {
		if _, ok := present[k]; !ok {
			return nil, contractErr("the result lacks %q", k)
		}
	}
	if err := r.check(); err != nil {
		return nil, err
	}
	return &r, nil
}

func (r *Result) check() error {
	if r.SchemaVersion != 1 {
		return contractErr("schemaVersion must be 1, got %d", r.SchemaVersion)
	}
	switch r.Outcome {
	case OutcomeChanged, OutcomeUnchanged, OutcomeBlocked, OutcomeFailed:
	default:
		return contractErr("unknown outcome %q", r.Outcome)
	}
	r.Summary = strings.TrimSpace(r.Summary)
	if r.Summary == "" {
		return contractErr("summary is empty")
	}
	if utf8.RuneCountInString(r.Summary) > maxSummaryRunes {
		r.Summary = string([]rune(r.Summary)[:maxSummaryRunes]) + "…"
	}
	if len(r.Report) > maxReportBytes {
		r.Report = truncateBytes(r.Report, maxReportBytes)
	}
	if k := r.Notification.Key; k != nil {
		*k = strings.TrimSpace(*k)
		if len(*k) > maxKeyLen {
			return contractErr("notification.key is longer than %d bytes", maxKeyLen)
		}
	}
	if r.Outcome == OutcomeBlocked && r.Question == nil {
		// A blocked run without a question still reaches the user, as a
		// failure-like notice, but it must say what blocks it.
		if r.Summary == "" {
			return contractErr("a blocked result must say what blocks it")
		}
	}
	if q := r.Question; q != nil {
		q.Text = strings.TrimSpace(q.Text)
		if q.Text == "" {
			return contractErr("question.text is empty")
		}
		if utf8.RuneCountInString(q.Text) > maxQuestionLen {
			return contractErr("question.text is too long")
		}
		if len(q.Options) > maxOptions {
			q.Options = q.Options[:maxOptions]
		}
	}
	if len(r.StatePatch) > maxPatchOps {
		return contractErr("statePatch has more than %d operations", maxPatchOps)
	}
	for i, op := range r.StatePatch {
		if !stateKeyPattern.MatchString(op.Key) {
			return contractErr("statePatch[%d].key %q must match %s", i, op.Key, stateKeyPattern)
		}
		switch op.Op {
		case "set":
			if op.Value == nil || !json.Valid([]byte(*op.Value)) {
				return contractErr("statePatch[%d] (set %s) needs a value that is JSON text", i, op.Key)
			}
		case "remove":
		default:
			return contractErr("statePatch[%d].op %q must be set or remove", i, op.Op)
		}
	}
	if len(r.Artifacts) > maxArtifacts {
		r.Artifacts = r.Artifacts[:maxArtifacts]
	}
	if r.NextWakeAt != nil && strings.TrimSpace(*r.NextWakeAt) != "" {
		if _, err := time.Parse(time.RFC3339, strings.TrimSpace(*r.NextWakeAt)); err != nil {
			return contractErr("nextWakeAt %q is not RFC 3339", *r.NextWakeAt)
		}
	}
	return nil
}

func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// ApplyPatch applies ops to state and returns the new state. Keys in locked
// (changed by the user while the run ran) are left alone and reported. The
// patch is rejected as a whole when the result would exceed the limits.
func ApplyPatch(state map[string]json.RawMessage, ops []PatchOp, locked map[string]bool) (map[string]json.RawMessage, []string, error) {
	out := map[string]json.RawMessage{}
	for k, v := range state {
		out[k] = v
	}
	var skipped []string
	for _, op := range ops {
		if locked[op.Key] {
			skipped = append(skipped, op.Key)
			continue
		}
		switch op.Op {
		case "set":
			var compact bytes.Buffer
			if err := json.Compact(&compact, []byte(*op.Value)); err != nil {
				return nil, nil, contractErr("statePatch value for %s: %v", op.Key, err)
			}
			out[op.Key] = json.RawMessage(compact.Bytes())
		case "remove":
			delete(out, op.Key)
		}
	}
	if err := checkState(out); err != nil {
		return nil, nil, err
	}
	sort.Strings(skipped)
	return out, skipped, nil
}

// checkState enforces the memory state limits.
func checkState(state map[string]json.RawMessage) error {
	if len(state) > MaxStateKeys {
		return fmt.Errorf("memory state has %d keys, more than %d", len(state), MaxStateKeys)
	}
	for k, v := range state {
		if !stateKeyPattern.MatchString(k) {
			return fmt.Errorf("memory state key %q must match %s", k, stateKeyPattern)
		}
		if !json.Valid(v) {
			return fmt.Errorf("memory state value of %q is not JSON", k)
		}
	}
	if b, _ := json.Marshal(state); len(b) > MaxStateBytes {
		return fmt.Errorf("memory state is %d bytes, more than %d", len(b), MaxStateBytes)
	}
	return nil
}

// FitSummary cuts a memory summary to MaxSummaryBytes. It reports whether
// it had to.
func FitSummary(s string) (string, bool) {
	if len(s) <= MaxSummaryBytes {
		return s, false
	}
	return truncateBytes(s, MaxSummaryBytes-len("…")), true
}

// DisplayText renders a run's final answer as the prose a session shows once
// the run's conversation is promoted: the summary, the report, and the
// question it asked. Agents leave the answer as contract JSON (Claude as a
// StructuredOutput tool call), which is unreadable in a transcript. ok is
// false when raw is not such an answer.
func DisplayText(raw []byte) (text string, ok bool) {
	raw = bytes.TrimSpace(raw)
	if bytes.HasPrefix(raw, []byte("```")) {
		if i := bytes.IndexByte(raw, '\n'); i > 0 {
			raw = bytes.TrimSpace(bytes.TrimSuffix(bytes.TrimSpace(raw[i+1:]), []byte("```")))
		}
	}
	if len(raw) == 0 || raw[0] != '{' {
		return "", false
	}
	var r Result
	if json.Unmarshal(raw, &r) != nil || r.SchemaVersion != 1 {
		return "", false
	}
	switch r.Outcome {
	case OutcomeChanged, OutcomeUnchanged, OutcomeBlocked, OutcomeFailed:
	default:
		return "", false
	}
	var parts []string
	for _, s := range []string{r.Summary, r.Report} {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(parts, s) {
			parts = append(parts, s)
		}
	}
	if q := r.Question; q != nil && strings.TrimSpace(q.Text) != "" {
		var b strings.Builder
		b.WriteString("**質問:** " + strings.TrimSpace(q.Text))
		for _, o := range q.Options {
			b.WriteString("\n- " + o)
		}
		parts = append(parts, b.String())
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "\n\n"), true
}
