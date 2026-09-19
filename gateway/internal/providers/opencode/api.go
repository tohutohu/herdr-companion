package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

type connection struct {
	URL, Username, Password string
	Version                 int
}

func (p *Provider) connection() (connection, error) {
	c := connection{URL: p.cfg.ServerURL, Username: p.cfg.Username, Password: p.cfg.Password, Version: p.cfg.ServerVersion}
	if c.URL != "" {
		if c.Version == 0 {
			c.Version = 1
		}
		if c.Version != 1 && c.Version != 2 {
			return c, fmt.Errorf("OpenCode serverVersion must be 1 or 2")
		}
		return c, nil
	}
	raw, err := os.ReadFile(filepath.Join(p.cfg.StateDir, "service.json"))
	if os.IsNotExist(err) {
		return c, providers.ErrUnsupported
	}
	if err != nil {
		return c, err
	}
	var reg struct {
		URL      string `json:"url"`
		Password string `json:"password"`
	}
	if err = json.Unmarshal(raw, &reg); err != nil {
		return c, fmt.Errorf("invalid OpenCode server registration")
	}
	u, err := url.Parse(reg.URL)
	if err != nil {
		return c, fmt.Errorf("invalid OpenCode server URL")
	}
	// A discovered registration may only send its password to the local daemon.
	host := u.Hostname()
	ip := net.ParseIP(host)
	if u.Scheme != "http" || u.User != nil || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
		return c, fmt.Errorf("OpenCode daemon must use a loopback HTTP URL")
	}
	c.URL = reg.URL
	c.Password = reg.Password
	c.Version = 2
	return c, nil
}
func (p *Provider) request(ctx context.Context, c connection, method, path, cwd string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+path, reader)
	if err != nil {
		return fmt.Errorf("invalid OpenCode server request")
	}
	req.Header.Set("Content-Type", "application/json")
	if cwd != "" && c.Version == 1 {
		req.Header.Set("x-opencode-directory", url.PathEscape(cwd))
	}
	if c.Password != "" {
		req.SetBasicAuth(c.Username, c.Password)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return fmt.Errorf("OpenCode server unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return providers.ErrInteractionGone
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("OpenCode server returned HTTP %d", resp.StatusCode)
	}
	if out == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return err
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}
func sessionPath(id string) string { return "/api/session/" + url.PathEscape(id) }

type pendingRequest struct {
	ID         string   `json:"id"`
	SessionID  string   `json:"sessionID"`
	Permission string   `json:"permission"`
	Action     string   `json:"action"`
	Patterns   []string `json:"patterns"`
	Resources  []string `json:"resources"`
	Questions  []struct {
		Question string         `json:"question"`
		Header   string         `json:"header"`
		Options  []model.Option `json:"options"`
		Multiple bool           `json:"multiple"`
		Custom   *bool          `json:"custom"`
	} `json:"questions"`
}

func (p *Provider) pending(ctx context.Context, c connection, id, cwd string) ([]model.Interaction, error) {
	var out []model.Interaction
	kinds := []string{"permission", "question"}
	if c.Version == 2 {
		kinds = []string{"permission"}
	}
	for _, kind := range kinds {
		path := "/" + kind
		var requests []pendingRequest
		if c.Version == 2 {
			path = sessionPath(id) + "/" + kind
			var envelope struct {
				Data []pendingRequest `json:"data"`
			}
			if err := p.request(ctx, c, "GET", path, cwd, nil, &envelope); err != nil {
				return nil, err
			}
			requests = envelope.Data
		} else {
			if err := p.request(ctx, c, "GET", path, cwd, nil, &requests); err != nil {
				return nil, err
			}
		}
		for _, r := range requests {
			if r.SessionID != id || r.ID == "" {
				continue
			}
			ia := model.Interaction{ID: "opencode-" + kind + ":" + r.ID, State: model.InteractionPending, Supported: true}
			if kind == "permission" {
				ia.Type = model.InteractionApproval
				ia.Title = r.Action
				if ia.Title == "" {
					ia.Title = r.Permission
				}
				resources := r.Resources
				if resources == nil {
					resources = r.Patterns
				}
				ia.Detail = strings.Join(resources, "\n")
				ia.Decisions = []string{model.DecisionApprove, model.DecisionDeny}
			} else {
				ia.Type = model.InteractionQuestions
				ia.Title = "OpenCode needs input"
				for i, q := range r.Questions {
					t := model.QuestionSelect
					if q.Multiple {
						t = model.QuestionMultiSelect
					}
					if len(q.Options) == 0 {
						t = model.QuestionText
					}
					ia.Questions = append(ia.Questions, model.Question{ID: questionID(i), Type: t, Question: q.Question, Header: q.Header, Options: q.Options, AllowOther: q.Custom == nil || *q.Custom})
				}
			}
			out = append(out, ia)
		}
	}
	if c.Version == 2 {
		forms, err := p.forms(ctx, c, id, cwd)
		if err != nil {
			return out, err
		}
		out = append(out, forms...)
	}
	return out, nil
}
func (p *Provider) Respond(ctx context.Context, id string, live *providers.Live, r model.InteractionResponse) error {
	if live == nil {
		return providers.ErrNotLive
	}
	c, err := p.connectionFor(ctx, id)
	if err != nil {
		return err
	}
	if c.Version == 2 && strings.HasPrefix(r.InteractionID, "opencode-form:") {
		return p.respondForm(ctx, c, id, live.Cwd, r)
	}
	pending, err := p.pending(ctx, c, id, live.Cwd)
	if err != nil {
		return err
	}
	for _, ia := range pending {
		if ia.ID != r.InteractionID {
			continue
		}
		kind := "question"
		var payload any
		if ia.Type == model.InteractionApproval {
			kind = "permission"
			reply := ""
			switch r.Decision {
			case model.DecisionApprove:
				reply = "once"
			case model.DecisionDeny:
				reply = "reject"
			default:
				return invalidAnswer()
			}
			payload = map[string]string{"reply": reply}
		} else {
			if len(r.Answers) != len(ia.Questions) {
				return invalidAnswer()
			}
			answers := make([][]string, len(ia.Questions))
			for i, q := range ia.Questions {
				a, ok := r.Answers[q.ID]
				if !ok {
					return invalidAnswer()
				}
				values := []string{}
				seen := map[string]bool{}
				for _, v := range a.Selected {
					valid := false
					for _, o := range q.Options {
						if o.Label == v {
							valid = true
							break
						}
					}
					if !valid || seen[v] {
						return invalidAnswer()
					}
					seen[v] = true
					values = append(values, v)
				}
				if a.Text != "" {
					if !q.AllowOther {
						return invalidAnswer()
					}
					values = append(values, a.Text)
				}
				if len(values) == 0 || (q.Type != model.QuestionMultiSelect && len(values) != 1) {
					return invalidAnswer()
				}
				answers[i] = values
			}
			payload = map[string]any{"answers": answers}
		}
		_, rid, _ := strings.Cut(ia.ID, ":")
		path := "/" + kind + "/" + url.PathEscape(rid) + "/reply"
		if c.Version == 2 {
			path = sessionPath(id) + path
		}
		return p.request(ctx, c, "POST", path, live.Cwd, payload, nil)
	}
	return providers.ErrInteractionGone
}
func (p *Provider) sendAPI(ctx context.Context, c connection, id, cwd string, in model.Input) error {
	if c.Version == 2 {
		files := []map[string]string{}
		for _, path := range append(append([]string{}, in.Images...), in.Files...) {
			uri := (&url.URL{Scheme: "file", Path: path}).String()
			files = append(files, map[string]string{"uri": uri, "name": filepath.Base(path)})
		}
		payload := map[string]any{"text": in.Text, "files": files}
		return p.request(ctx, c, "POST", sessionPath(id)+"/prompt", cwd, payload, nil)
	}
	parts := []map[string]string{}
	if in.Text != "" {
		parts = append(parts, map[string]string{"type": "text", "text": in.Text})
	}
	for _, path := range append(append([]string{}, in.Images...), in.Files...) {
		typ := mime.TypeByExtension(filepath.Ext(path))
		if typ == "" {
			typ = "application/octet-stream"
		}
		parts = append(parts, map[string]string{"type": "file", "mime": typ, "url": (&url.URL{Scheme: "file", Path: path}).String(), "filename": filepath.Base(path)})
	}
	return p.request(ctx, c, "POST", "/session/"+url.PathEscape(id)+"/prompt_async", cwd, map[string]any{"parts": parts}, nil)
}

// A v1 TUI must not be sent to an unrelated v2 daemon simply because both
// versions are installed. The owning session table identifies the protocol.
func (p *Provider) connectionFor(ctx context.Context, id string) (connection, error) {
	c, err := p.connection()
	if err != nil {
		return c, err
	}
	if p.cfg.ServerURL != "" {
		return c, nil
	}
	db, err := p.database()
	if err != nil {
		return c, err
	}
	defer db.Close()
	table, err := findTable(ctx, db, id)
	if err != nil {
		return c, err
	}
	if table != "session_v2" {
		return c, providers.ErrUnsupported
	}
	return c, nil
}
