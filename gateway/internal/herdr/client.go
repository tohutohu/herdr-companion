// Package herdr is a small client for the Herdr socket API
// (newline-delimited JSON over a Unix domain socket).
// Only the handful of methods the gateway needs are wrapped.
package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

// Agent statuses reported by Herdr.
const (
	StatusIdle    = "idle"
	StatusWorking = "working"
	StatusBlocked = "blocked"
	StatusDone    = "done"
	StatusUnknown = "unknown"
)

type AgentSession struct {
	Source string `json:"source"`
	Agent  string `json:"agent"`
	Kind   string `json:"kind"` // "id" | "path"
	Value  string `json:"value"`
}

type Pane struct {
	PaneID        string        `json:"pane_id"`
	WorkspaceID   string        `json:"workspace_id"`
	TabID         string        `json:"tab_id"`
	Agent         *string       `json:"agent"`
	AgentStatus   string        `json:"agent_status"`
	AgentSession  *AgentSession `json:"agent_session"`
	Cwd           *string       `json:"cwd"`
	ForegroundCwd *string       `json:"foreground_cwd"`
	Title         *string       `json:"title"`
	Label         *string       `json:"label"`
	Revision      uint64        `json:"revision"`
}

func (p Pane) AgentName() string { return deref(p.Agent) }

// WorkingDir prefers the agent process cwd.
func (p Pane) WorkingDir() string {
	if s := deref(p.ForegroundCwd); s != "" {
		return s
	}
	return deref(p.Cwd)
}

type Workspace struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}

type Snapshot struct {
	Version    string      `json:"version"`
	Protocol   int         `json:"protocol"`
	Workspaces []Workspace `json:"workspaces"`
	Panes      []Pane      `json:"panes"`
}

func (s *Snapshot) WorkspaceLabel(id string) string {
	for _, w := range s.Workspaces {
		if w.WorkspaceID == id {
			return w.Label
		}
	}
	return ""
}

type ReadResult struct {
	PaneID    string `json:"pane_id"`
	Text      string `json:"text"`
	Revision  uint64 `json:"revision"`
	Truncated bool   `json:"truncated"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return "herdr: " + e.Code + ": " + e.Message }

type Client struct {
	socket string
	seq    atomic.Uint64
}

// DefaultSocket follows Herdr's resolution order (HERDR_SOCKET_PATH, HERDR_SESSION, default).
func DefaultSocket() string {
	if p := os.Getenv("HERDR_SOCKET_PATH"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	if s := os.Getenv("HERDR_SESSION"); s != "" {
		return filepath.Join(home, ".config", "herdr", "sessions", s, "herdr.sock")
	}
	return filepath.Join(home, ".config", "herdr", "herdr.sock")
}

func New(socket string) *Client {
	if socket == "" {
		socket = DefaultSocket()
	}
	return &Client{socket: socket}
}

func (c *Client) Socket() string { return c.socket }

type request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
}

type response struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
}

func (c *Client) nextID() string { return "hm_" + strconv.FormatUint(c.seq.Add(1), 10) }

func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return nil, fmt.Errorf("herdr: connect %s: %w", c.socket, err)
	}
	return conn, nil
}

// Call performs one request on a fresh connection and decodes result into out.
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	if params == nil {
		params = struct{}{}
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	} else {
		conn.SetDeadline(time.Now().Add(15 * time.Second))
	}
	id := c.nextID()
	if err := json.NewEncoder(conn).Encode(request{ID: id, Method: method, Params: params}); err != nil {
		return err
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 64<<20)
	for sc.Scan() {
		var resp response
		if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
			return fmt.Errorf("herdr: decode %s: %w", method, err)
		}
		if resp.ID != id {
			continue
		}
		if resp.Error != nil {
			return resp.Error
		}
		if out != nil {
			return json.Unmarshal(resp.Result, out)
		}
		return nil
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return fmt.Errorf("herdr: %s: connection closed", method)
}

func (c *Client) Snapshot(ctx context.Context) (*Snapshot, error) {
	var r struct {
		Snapshot Snapshot `json:"snapshot"`
	}
	if err := c.Call(ctx, "session.snapshot", nil, &r); err != nil {
		return nil, err
	}
	return &r.Snapshot, nil
}

// ReadPane returns recent terminal text (ANSI stripped, soft wraps joined).
func (c *Client) ReadPane(ctx context.Context, paneID string, lines int) (*ReadResult, error) {
	var r struct {
		Read ReadResult `json:"read"`
	}
	params := map[string]any{"pane_id": paneID, "source": "recent_unwrapped", "lines": lines, "format": "text", "strip_ansi": true}
	if err := c.Call(ctx, "pane.read", params, &r); err != nil {
		return nil, err
	}
	return &r.Read, nil
}

// SendKeys sends Herdr key-combo strings such as "enter", "esc", "down", "ctrl+c".
func (c *Client) SendKeys(ctx context.Context, paneID string, keys ...string) error {
	return c.Call(ctx, "pane.send_keys", map[string]any{"pane_id": paneID, "keys": keys}, nil)
}

func (c *Client) SendText(ctx context.Context, paneID, text string) error {
	return c.Call(ctx, "pane.send_text", map[string]any{"pane_id": paneID, "text": text}, nil)
}

// Prompt submits text followed by Enter through the agent surface. Herdr
// rejects it with agent_blocked when the agent shows a dialog.
func (c *Client) Prompt(ctx context.Context, paneID, text string) error {
	return c.Call(ctx, "agent.prompt", map[string]any{"target": paneID, "text": text}, nil)
}

// Event is one pushed subscription line.
type Event struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

type AgentStatusChanged struct {
	PaneID      string  `json:"pane_id"`
	WorkspaceID string  `json:"workspace_id"`
	AgentStatus string  `json:"agent_status"`
	Agent       *string `json:"agent"`
}

// Subscribe opens a long-lived subscription. The returned channel is closed
// when the connection ends or ctx is cancelled.
func (c *Client) Subscribe(ctx context.Context, subscriptions []map[string]any) (<-chan Event, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	id := c.nextID()
	req := request{ID: id, Method: "events.subscribe", Params: map[string]any{"subscriptions": subscriptions}}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		conn.Close()
		return nil, err
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if !sc.Scan() {
		conn.Close()
		return nil, fmt.Errorf("herdr: subscribe: no acknowledgement: %v", sc.Err())
	}
	var ack response
	if err := json.Unmarshal(sc.Bytes(), &ack); err != nil {
		conn.Close()
		return nil, err
	}
	if ack.Error != nil {
		conn.Close()
		return nil, ack.Error
	}
	conn.SetReadDeadline(time.Time{})

	ch := make(chan Event, 64)
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	go func() {
		defer close(ch)
		defer conn.Close()
		for sc.Scan() {
			var ev Event
			if err := json.Unmarshal(sc.Bytes(), &ev); err != nil || ev.Event == "" {
				continue
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// CreateWorkspace opens a new workspace (without focusing it) and returns its root pane.
func (c *Client) CreateWorkspace(ctx context.Context, cwd, label string) (workspaceID, paneID string, err error) {
	var r struct {
		Workspace Workspace `json:"workspace"`
		RootPane  Pane      `json:"root_pane"`
	}
	params := map[string]any{"cwd": cwd, "label": label, "focus": false}
	if err := c.Call(ctx, "workspace.create", params, &r); err != nil {
		return "", "", err
	}
	return r.Workspace.WorkspaceID, r.RootPane.PaneID, nil
}

// StartAgent launches an agent in a shell pane. Herdr returns agent_not_ready
// when the agent is blocked by a startup dialog.
func (c *Client) StartAgent(ctx context.Context, name, kind, paneID string, args []string, timeout time.Duration) error {
	if args == nil {
		args = []string{} // herdr rejects null
	}
	params := map[string]any{"name": name, "kind": kind, "pane_id": paneID, "args": args, "timeout_ms": timeout.Milliseconds()}
	return c.Call(ctx, "agent.start", params, nil)
}

// ReadVisible returns the currently rendered screen of a pane.
func (c *Client) ReadVisible(ctx context.Context, paneID string) (string, error) {
	var r struct {
		Read ReadResult `json:"read"`
	}
	params := map[string]any{"pane_id": paneID, "source": "visible", "format": "text", "strip_ansi": true}
	if err := c.Call(ctx, "pane.read", params, &r); err != nil {
		return "", err
	}
	return r.Read.Text, nil
}

// Pane returns one pane from a fresh snapshot.
func (c *Client) Pane(ctx context.Context, paneID string) (*Pane, error) {
	snap, err := c.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	for i := range snap.Panes {
		if snap.Panes[i].PaneID == paneID {
			return &snap.Panes[i], nil
		}
	}
	return nil, &Error{Code: "pane_not_found", Message: paneID}
}
