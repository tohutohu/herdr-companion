package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Codex app-server speaks JSON-RPC 2.0 without the "jsonrpc" field.

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("codex app-server: %s (%d)", e.Message, e.Code) }

type wireMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

// transport sends and receives whole JSON messages.
type transport interface {
	write(ctx context.Context, b []byte) error
	read(ctx context.Context) ([]byte, error)
	close() error
}

// Handler receives server-initiated traffic.
type Handler interface {
	Notification(method string, params json.RawMessage)
	Request(id json.RawMessage, method string, params json.RawMessage)
	Closed(err error)
}

type rpcClient struct {
	t       transport
	h       Handler
	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[string]chan wireMessage
	done    chan struct{}
	err     error
}

func newRPCClient(t transport, h Handler) *rpcClient {
	c := &rpcClient{t: t, h: h, pending: map[string]chan wireMessage{}, done: make(chan struct{})}
	go c.loop()
	return c
}

func (c *rpcClient) loop() {
	var err error
	for {
		var b []byte
		b, err = c.t.read(context.Background())
		if err != nil {
			break
		}
		var m wireMessage
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			if c.h != nil {
				c.h.Request(m.ID, m.Method, m.Params)
			}
		case m.Method != "":
			if c.h != nil {
				c.h.Notification(m.Method, m.Params)
			}
		case len(m.ID) > 0:
			c.mu.Lock()
			ch := c.pending[string(m.ID)]
			delete(c.pending, string(m.ID))
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
	}
	c.mu.Lock()
	c.err = err
	for _, ch := range c.pending {
		close(ch)
	}
	c.pending = map[string]chan wireMessage{}
	c.mu.Unlock()
	close(c.done)
	if c.h != nil {
		c.h.Closed(err)
	}
}

func (c *rpcClient) alive() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

func (c *rpcClient) call(ctx context.Context, method string, params, out any) error {
	id := c.nextID.Add(1)
	idRaw := json.RawMessage(strconv.FormatInt(id, 10))
	ch := make(chan wireMessage, 1)
	c.mu.Lock()
	if !c.alive() {
		c.mu.Unlock()
		return fmt.Errorf("codex app-server connection closed: %v", c.err)
	}
	c.pending[string(idRaw)] = ch
	c.mu.Unlock()

	b, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	if err := c.t.write(ctx, b); err != nil {
		c.mu.Lock()
		delete(c.pending, string(idRaw))
		c.mu.Unlock()
		return err
	}
	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, string(idRaw))
		c.mu.Unlock()
		return ctx.Err()
	case m, ok := <-ch:
		if !ok {
			return errors.New("codex app-server connection closed")
		}
		if m.Error != nil {
			return m.Error
		}
		if out != nil {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	}
}

func (c *rpcClient) notify(ctx context.Context, method string, params any) error {
	b, err := json.Marshal(map[string]any{"method": method, "params": params})
	if err != nil {
		return err
	}
	return c.t.write(ctx, b)
}

// respond answers a server-initiated request.
func (c *rpcClient) respond(ctx context.Context, id json.RawMessage, result any) error {
	b, err := json.Marshal(map[string]any{"id": id, "result": result})
	if err != nil {
		return err
	}
	return c.t.write(ctx, b)
}

func (c *rpcClient) initialize(ctx context.Context) error {
	params := map[string]any{
		"clientInfo":   map[string]string{"name": "herdr_mobile", "title": "Herdr Mobile Gateway", "version": "0.1.0"},
		"capabilities": map[string]any{"experimentalApi": true},
	}
	if err := c.call(ctx, "initialize", params, nil); err != nil {
		return err
	}
	return c.notify(ctx, "initialized", map[string]any{})
}

func (c *rpcClient) close() { c.t.close() }

// --- stdio transport: a child `codex app-server` process ---

type stdioTransport struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	sc    *bufio.Scanner
	wmu   sync.Mutex
}

func startStdio(bin string) (*stdioTransport, error) {
	cmd := exec.Command(bin, "app-server")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s app-server: %w", bin, err)
	}
	go cmd.Wait()
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	return &stdioTransport{cmd: cmd, stdin: stdin, sc: sc}, nil
}

func (s *stdioTransport) write(_ context.Context, b []byte) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, err := s.stdin.Write(append(b, '\n'))
	return err
}

func (s *stdioTransport) read(context.Context) ([]byte, error) {
	if !s.sc.Scan() {
		if err := s.sc.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	return append([]byte(nil), s.sc.Bytes()...), nil
}

func (s *stdioTransport) close() error {
	s.stdin.Close()
	if s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
	return nil
}

// --- unix socket transport: WebSocket over the shared daemon control socket ---

type wsTransport struct {
	c *websocket.Conn
}

func dialDaemon(ctx context.Context, socket string) (*wsTransport, error) {
	httpClient := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}}
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(dctx, "ws://localhost/rpc", &websocket.DialOptions{HTTPClient: httpClient})
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(256 << 20)
	return &wsTransport{c: c}, nil
}

func (w *wsTransport) write(ctx context.Context, b []byte) error {
	return w.c.Write(ctx, websocket.MessageText, b)
}

func (w *wsTransport) read(ctx context.Context) ([]byte, error) {
	_, b, err := w.c.Read(ctx)
	return b, err
}

func (w *wsTransport) close() error { return w.c.Close(websocket.StatusNormalClosure, "") }
