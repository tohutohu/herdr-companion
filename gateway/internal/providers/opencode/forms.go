package opencode

import (
	"context"
	"net/url"
	"strings"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

type formField struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Hidden      bool   `json:"hidden"`
	When        []any  `json:"when"`
	Custom      bool   `json:"custom"`
	Options     []struct {
		Value       string `json:"value"`
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
}
type form struct {
	ID        string      `json:"id"`
	SessionID string      `json:"sessionID"`
	Title     string      `json:"title"`
	Fields    []formField `json:"fields"`
	State     struct {
		Status string `json:"status"`
	} `json:"state"`
}

func (f form) interaction() model.Interaction {
	ia := model.Interaction{ID: "opencode-form:" + f.ID, Type: model.InteractionQuestions, State: model.InteractionPending, Supported: len(f.Fields) > 0, Title: f.Title}
	keys := map[string]bool{}
	for _, field := range f.Fields {
		if field.Hidden || len(field.When) > 0 || (field.Type != "string" && field.Type != "multiselect") || keys[field.Key] || field.Key == "" {
			ia.Supported = false
		}
		keys[field.Key] = true
		q := model.Question{ID: field.Key, Question: field.Title, Header: field.Title, AllowOther: field.Custom, Type: model.QuestionSelect}
		if field.Description != "" {
			if q.Question != "" {
				q.Question += "\n"
			}
			q.Question += field.Description
		}
		if field.Type == "multiselect" {
			q.Type = model.QuestionMultiSelect
		} else if len(field.Options) == 0 {
			q.Type = model.QuestionText
			q.AllowOther = true
		}
		labels := map[string]bool{}
		for _, o := range field.Options {
			if labels[o.Label] {
				ia.Supported = false
			}
			labels[o.Label] = true
			q.Options = append(q.Options, model.Option{Label: o.Label, Description: o.Description})
		}
		ia.Questions = append(ia.Questions, q)
	}
	if !ia.Supported {
		ia.Detail = "Open the terminal to answer this form."
	}
	return ia
}
func (p *Provider) forms(ctx context.Context, c connection, id, cwd string) ([]model.Interaction, error) {
	var env struct {
		Data []form `json:"data"`
	}
	if err := p.request(ctx, c, "GET", sessionPath(id)+"/form", cwd, nil, &env); err != nil {
		return nil, err
	}
	var out []model.Interaction
	for _, f := range env.Data {
		if f.SessionID == id && f.ID != "" {
			out = append(out, f.interaction())
		}
	}
	return out, nil
}
func (p *Provider) respondForm(ctx context.Context, c connection, id, cwd string, r model.InteractionResponse) error {
	fid := strings.TrimPrefix(r.InteractionID, "opencode-form:")
	path := sessionPath(id) + "/form/" + url.PathEscape(fid)
	var env struct {
		Data form `json:"data"`
	}
	if err := p.request(ctx, c, "GET", path, cwd, nil, &env); err != nil {
		return err
	}
	f := env.Data
	if f.SessionID != id || f.ID != fid || f.State.Status != "pending" {
		return providers.ErrInteractionGone
	}
	ia := f.interaction()
	if !ia.Supported {
		return providers.ErrUnsupported
	}
	if len(r.Answers) != len(f.Fields) {
		return invalidAnswer()
	}
	answer := map[string]any{}
	for i, field := range f.Fields {
		a, ok := r.Answers[field.Key]
		if !ok {
			return invalidAnswer()
		}
		values := []string{}
		seen := map[string]bool{}
		for _, label := range a.Selected {
			found := false
			for _, opt := range field.Options {
				if opt.Label == label {
					if seen[opt.Value] {
						return invalidAnswer()
					}
					seen[opt.Value] = true
					values = append(values, opt.Value)
					found = true
					break
				}
			}
			if !found {
				return invalidAnswer()
			}
		}
		if a.Text != "" {
			if !ia.Questions[i].AllowOther {
				return invalidAnswer()
			}
			values = append(values, a.Text)
		}
		if field.Type == "multiselect" {
			answer[field.Key] = values
		} else {
			if len(values) != 1 {
				return invalidAnswer()
			}
			answer[field.Key] = values[0]
		}
	}
	return p.request(ctx, c, "POST", path+"/reply", cwd, map[string]any{"answer": answer}, nil)
}
