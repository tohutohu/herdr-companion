// Package opencode reads OpenCode v1 and official v2 history without modifying
// provider storage. Both versions retain the same Herdr agent/session identity.
package opencode

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/deadletter"
	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers"
)

type Config struct {
	Database string `json:"database,omitempty"`
	Binary   string `json:"binary,omitempty"`
	// ServerURL connects to an existing v1 or v2 server; empty discovers v2's
	// registered local daemon. It never starts or replaces a provider process.
	ServerURL     string `json:"serverUrl,omitempty"`
	ServerVersion int    `json:"serverVersion,omitempty"`
	StateDir      string `json:"stateDir,omitempty"`
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
}

type Provider struct {
	cfg    Config
	dbPath string
	term   providers.Terminal
	sink   deadletter.Sink
	http   *http.Client
}

func New(cfg Config, term providers.Terminal, sink deadletter.Sink) *Provider {
	if cfg.Database == "" {
		cfg.Database = defaultDatabase()
	}
	if cfg.Binary == "" {
		cfg.Binary = "opencode"
	}
	if cfg.StateDir == "" {
		cfg.StateDir = defaultPath("XDG_STATE_HOME", ".local/state")
	}
	if cfg.Username == "" {
		cfg.Username = "opencode"
	}
	if sink == nil {
		sink = deadletter.Nop{}
	}
	return &Provider{cfg: cfg, dbPath: cfg.Database, term: term, sink: sink, http: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (*Provider) Name() string        { return "opencode" }
func (*Provider) DisplayName() string { return "OpenCode" }
func (*Provider) HerdrAgent() string  { return "opencode" }

func (p *Provider) history(ctx context.Context, id string) (*providers.Summary, conversion, error) {
	db, err := p.database()
	if err != nil {
		return nil, conversion{}, err
	}
	defer db.Close()
	table, err := findTable(ctx, db, id)
	if err != nil {
		return nil, conversion{}, err
	}
	s, err := sessionSummary(ctx, db, id, table)
	if err != nil {
		return nil, conversion{}, err
	}
	rs, err := records(ctx, db, id, table)
	if err != nil {
		return nil, conversion{}, err
	}
	c := p.convert(id, rs)
	s.Model = c.Model
	s.Effort = c.Effort
	s.Mode = c.Mode
	s.Cost = c.Cost
	s.LastTurnFailed = c.Failed
	for i := len(c.Messages) - 1; i >= 0; i-- {
		for _, b := range c.Messages[i].Blocks {
			if b.Type == model.BlockText && b.Text != "" {
				s.LastMessage = providers.OneLine(b.Text, 200)
				return s, c, nil
			}
		}
	}
	return s, c, nil
}
func (p *Provider) Summary(ctx context.Context, id string, live *providers.Live) (*providers.Summary, error) {
	s, _, err := p.history(ctx, id)
	if err != nil {
		return nil, err
	}
	if live != nil {
		if conn, err := p.connectionFor(ctx, id); err == nil {
			if pending, err := p.pending(ctx, conn, id, live.Cwd); err == nil {
				for _, ia := range pending {
					s.Pending = ia.Type
					if ia.Type == model.InteractionApproval {
						break
					}
				}
			}
		}
	}
	return s, nil
}
func (p *Provider) Messages(ctx context.Context, id string, live *providers.Live) ([]model.Message, error) {
	_, c, err := p.history(ctx, id)
	if err != nil {
		return nil, err
	}
	if live != nil {
		var pending []model.Interaction
		if conn, e := p.connectionFor(ctx, id); e == nil {
			pending, e = p.pending(ctx, conn, id, live.Cwd)
			if e != nil {
				p.sink.Record(p.Name(), p.Name()+":"+id, deadletter.ProviderError, "read pending interactions", nil)
			}
		}
		for _, ia := range pending {
			ia := ia
			c.Messages = append(c.Messages, model.Message{ID: ia.ID, Role: model.RoleAssistant, Blocks: []model.Block{{Type: model.BlockInteraction, Interaction: &ia}}})
		}
		if live.Blocked() && len(pending) == 0 {
			c.Messages = append(c.Messages, model.Message{ID: "opencode-terminal", Role: model.RoleAssistant, Blocks: []model.Block{{Type: model.BlockInteraction, Interaction: &model.Interaction{ID: "opencode-terminal", Type: model.InteractionQuestions, State: model.InteractionPending, Title: "OpenCode needs input", Detail: "Open the terminal to answer this dialog.", Supported: false}}}})
		}
	}
	return c.Messages, nil
}
func (p *Provider) Image(ctx context.Context, id, message string, index int) (string, []byte, error) {
	_, c, err := p.history(ctx, id)
	if err != nil {
		return "", nil, err
	}
	images := c.Images[message]
	if index < 0 || index >= len(images) {
		return "", nil, providers.ErrNotFound
	}
	return images[index].Mime, images[index].Data, nil
}
func (p *Provider) Send(ctx context.Context, id string, live *providers.Live, in model.Input) error {
	if live == nil {
		return providers.ErrNotLive
	}
	if strings.TrimSpace(in.Text) == "" && len(in.Images) == 0 && len(in.Files) == 0 {
		return fmt.Errorf("empty message")
	}
	conn, err := p.connectionFor(ctx, id)
	if err == nil {
		return p.sendAPI(ctx, conn, id, live.Cwd, in)
	}
	if !errors.Is(err, providers.ErrUnsupported) {
		return err
	}
	// Plain prompts use Herdr's blocked-dialog guard. Attachments need the API;
	// silently treating image paths as text would lose their intended semantics.
	if len(in.Images) > 0 {
		return fmt.Errorf("OpenCode image attachments require a connected server")
	}
	return p.term.Prompt(ctx, live.PaneID, providers.TextWithFiles(in))
}
func (p *Provider) LaunchArgs(opts providers.LaunchOptions) []string {
	args := []string{}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	return args
}
func (p *Provider) ResumeArgs(id, cwd string) []string { return []string{"--session", id} }
func (p *Provider) StartupKeys(string) []string        { return nil }
func (p *Provider) Models(ctx context.Context) (providers.ModelCatalog, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, p.cfg.Binary, "models").Output()
	if err != nil {
		return providers.ModelCatalog{}, fmt.Errorf("OpenCode models: %w", err)
	}
	out := providers.ModelCatalog{Models: []providers.ModelOption{}}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		id := strings.TrimSpace(line)
		if strings.Contains(id, "/") && providers.ValidModelID(id) && !seen[id] {
			seen[id] = true
			out.Models = append(out.Models, providers.ModelOption{ID: id, Name: id})
		}
	}
	return out, nil
}
func (p *Provider) LocateLaunched(ctx context.Context, cwd string, since time.Time) string {
	db, err := p.database()
	if err != nil {
		return ""
	}
	defer db.Close()
	var id string
	tables, err := sessionTables(ctx, db)
	if err != nil {
		return ""
	}
	found := map[string]bool{}
	for _, table := range tables {
		rows, err := db.QueryContext(ctx, "SELECT id FROM "+table+" WHERE directory=? AND time_created>=? AND parent_id IS NULL LIMIT 2", cwd, since.UnixMilli())
		if err != nil {
			return ""
		}
		for rows.Next() {
			if rows.Scan(&id) != nil {
				rows.Close()
				return ""
			}
			found[id] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return ""
		}
	}
	if len(found) != 1 {
		return ""
	}
	for id := range found {
		return id
	}
	return ""
}

// Official v2.0.x moved model selection out of the root TUI CLI. Create the
// session with its model through the shared server, then attach by native id.
func (p *Provider) PrepareLaunch(ctx context.Context, opts providers.LaunchOptions) ([]string, string, error) {
	if opts.Effort != "" {
		return nil, "", fmt.Errorf("OpenCode launch does not accept a separate reasoning effort")
	}
	if opts.Model == "" {
		return []string{}, "", nil
	}
	check, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(check, p.cfg.Binary, "--version").Output()
	if err != nil {
		return nil, "", fmt.Errorf("OpenCode version: %w", err)
	}
	version := strings.TrimSpace(string(raw))
	version = strings.TrimPrefix(version, "opencode ")
	version = strings.TrimPrefix(version, "v")
	if !strings.HasPrefix(version, "2.") {
		return p.LaunchArgs(opts), "", nil
	}
	providerID, modelID, ok := strings.Cut(opts.Model, "/")
	if !ok || modelID == "" {
		return nil, "", fmt.Errorf("OpenCode model must be provider/model")
	}
	c, err := p.connection()
	if err != nil {
		return nil, "", fmt.Errorf("start OpenCode v2 once before choosing a model: %w", err)
	}
	if c.Version != 2 {
		return nil, "", fmt.Errorf("OpenCode v2 requires a v2 server")
	}
	var env struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	payload := map[string]any{"model": map[string]string{"providerID": providerID, "id": modelID}, "location": map[string]string{"directory": opts.Cwd}}
	if err := p.request(ctx, c, "POST", "/api/session", opts.Cwd, payload, &env); err != nil {
		return nil, "", err
	}
	if env.Data.ID == "" {
		return nil, "", fmt.Errorf("OpenCode server returned no session id")
	}
	return p.ResumeArgs(env.Data.ID, opts.Cwd), env.Data.ID, nil
}
