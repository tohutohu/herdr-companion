package codex

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

type userInputParams struct {
	ThreadID  string `json:"threadId"`
	Questions []struct {
		ID       string `json:"id"`
		Header   string `json:"header"`
		Question string `json:"question"`
		IsOther  bool   `json:"isOther"`
		IsSecret bool   `json:"isSecret"`
		Options  []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"questions"`
}

type approvalParams struct {
	ThreadID               string          `json:"threadId"`
	Reason                 *string         `json:"reason"`
	Command                json.RawMessage `json:"command"` // string (v2) or []string (legacy)
	Cwd                    *string         `json:"cwd"`
	GrantRoot              *string         `json:"grantRoot"`
	NetworkApprovalContext *struct {
		Host     string `json:"host"`
		Protocol string `json:"protocol"`
	} `json:"networkApprovalContext"`
	Permissions json.RawMessage `json:"permissions"`
	Message     string          `json:"message"`
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func commandString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []string
	if json.Unmarshal(raw, &parts) == nil {
		return strings.Join(parts, " ")
	}
	return ""
}

var allDecisions = []string{model.DecisionApprove, model.DecisionApproveSession, model.DecisionDeny}

// interactionFor converts a pending server request. Returns nil for
// requests that are not user-facing.
func interactionFor(r pendingRequest, sink deadletter.Sink) *model.Interaction {
	id := requestIDPrefix + string(r.ID)
	var ap approvalParams
	json.Unmarshal(r.Params, &ap)
	thread := ap.ThreadID
	base := model.Interaction{ID: id, State: model.InteractionPending, Supported: true}

	switch r.Method {
	case "item/tool/requestUserInput":
		var p userInputParams
		if err := json.Unmarshal(r.Params, &p); err != nil || len(p.Questions) == 0 {
			sink.Record(providerName, gatewayID(p.ThreadID), deadletter.UnsupportedInteraction, "unreadable requestUserInput", r.Params)
			base.Type, base.Title, base.Supported = model.InteractionQuestions, "Codex needs input", false
			return &base
		}
		base.Type, base.Title = model.InteractionQuestions, "Codex needs input"
		for _, q := range p.Questions {
			mq := model.Question{ID: q.ID, Type: model.QuestionSelect, Header: q.Header, Question: q.Question, AllowOther: q.IsOther}
			for _, o := range q.Options {
				mq.Options = append(mq.Options, model.Option{Label: o.Label, Description: o.Description})
			}
			if len(mq.Options) == 0 {
				mq.Type = model.QuestionText
			}
			base.Questions = append(base.Questions, mq)
		}
		return &base

	case "item/commandExecution/requestApproval", "execCommandApproval":
		base.Type, base.Title, base.Decisions = model.InteractionApproval, "Run command?", allDecisions
		detail := commandString(ap.Command)
		if ap.NetworkApprovalContext != nil {
			base.Title = "Allow network access?"
			detail = ap.NetworkApprovalContext.Protocol + " " + ap.NetworkApprovalContext.Host
		}
		if c := str(ap.Cwd); c != "" && detail != "" {
			detail += "\n(in " + c + ")"
		}
		base.Detail = joinLines(detail, str(ap.Reason))
		return &base

	case "item/fileChange/requestApproval", "applyPatchApproval":
		base.Type, base.Title, base.Decisions = model.InteractionApproval, "Apply file changes?", allDecisions
		base.Detail = joinLines(str(ap.Reason), prefixed("Grant write access to ", str(ap.GrantRoot)))
		return &base

	case "item/permissions/requestApproval":
		base.Type, base.Title, base.Decisions = model.InteractionApproval, "Grant additional permissions?", allDecisions
		base.Detail = joinLines(str(ap.Reason), compact(ap.Permissions, 600))
		return &base

	case "mcpServer/elicitation/request":
		base.Type, base.Title, base.Supported = model.InteractionQuestions, "MCP server request", false
		base.Detail = ap.Message
		sink.Record(providerName, gatewayID(thread), deadletter.UnsupportedInteraction, "mcp elicitation is answered in the terminal", r.Params)
		return &base

	default:
		sink.Record(providerName, gatewayID(thread), deadletter.UnsupportedInteraction, "unsupported server request: "+r.Method, r.Params)
		base.Type, base.Title, base.Supported = model.InteractionApproval, "Codex request: "+r.Method, false
		return &base
	}
}

func joinLines(parts ...string) string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n")
}

func prefixed(prefix, s string) string {
	if s == "" {
		return ""
	}
	return prefix + s
}

// responseFor builds the JSON-RPC result for a server request.
func responseFor(r pendingRequest, resp model.InteractionResponse) (any, error) {
	switch r.Method {
	case "item/tool/requestUserInput":
		var p userInputParams
		if err := json.Unmarshal(r.Params, &p); err != nil {
			return nil, err
		}
		answers := map[string]any{}
		for _, q := range p.Questions {
			a, ok := resp.Answers[q.ID]
			if !ok {
				return nil, fmt.Errorf("missing answer for question %s", q.ID)
			}
			values := append([]string{}, a.Selected...)
			if t := strings.TrimSpace(a.Text); t != "" {
				values = append(values, t)
			}
			if len(values) == 0 {
				return nil, fmt.Errorf("empty answer for question %s", q.ID)
			}
			answers[q.ID] = map[string]any{"answers": values}
		}
		return map[string]any{"answers": answers}, nil

	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		decision, err := pick(resp.Decision, "accept", "acceptForSession", "decline")
		if err != nil {
			return nil, err
		}
		return map[string]any{"decision": decision}, nil

	case "execCommandApproval", "applyPatchApproval":
		decision, err := pick(resp.Decision, "approved", "approved_for_session", "denied")
		if err != nil {
			return nil, err
		}
		return map[string]any{"decision": decision}, nil

	case "item/permissions/requestApproval":
		var p approvalParams
		json.Unmarshal(r.Params, &p)
		switch resp.Decision {
		case model.DecisionApprove:
			return map[string]any{"permissions": p.Permissions, "scope": "turn"}, nil
		case model.DecisionApproveSession:
			return map[string]any{"permissions": p.Permissions, "scope": "session"}, nil
		case model.DecisionDeny:
			return map[string]any{"permissions": map[string]any{}}, nil
		}
		return nil, fmt.Errorf("unsupported decision %q", resp.Decision)
	}
	return nil, fmt.Errorf("%s: %w", r.Method, providers.ErrUnsupported)
}

func pick(decision, approve, session, deny string) (string, error) {
	switch decision {
	case model.DecisionApprove:
		return approve, nil
	case model.DecisionApproveSession:
		return session, nil
	case model.DecisionDeny:
		return deny, nil
	}
	return "", fmt.Errorf("unsupported decision %q", decision)
}
