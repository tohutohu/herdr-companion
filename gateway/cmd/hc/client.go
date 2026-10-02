package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

type connection struct {
	baseURL string
	token   string
}

// resolveConnection finds the Gateway: flags, then HC_URL/HC_TOKEN, then
// the local Gateway's own config file (the usual case on the Mac).
func resolveConnection(flagURL, flagToken string) (connection, error) {
	c := connection{baseURL: flagURL, token: flagToken}
	if c.baseURL == "" {
		c.baseURL = os.Getenv("HC_URL")
	}
	if c.token == "" {
		c.token = os.Getenv("HC_TOKEN")
	}
	if c.baseURL == "" || c.token == "" {
		listen, token, path, err := localGatewayConfig()
		if err != nil {
			return c, fmt.Errorf("no Gateway configured: set HC_URL and HC_TOKEN (%v)", err)
		}
		if c.baseURL == "" {
			c.baseURL = listenURL(listen)
		}
		if c.token == "" {
			c.token = token
		}
		if c.baseURL == "" || c.token == "" {
			return c, fmt.Errorf("%s has no listen address or token; set HC_URL and HC_TOKEN", path)
		}
	}
	c.baseURL = strings.TrimRight(c.baseURL, "/")
	return c, nil
}

func localGatewayConfig() (listen, token, path string, err error) {
	var candidates []string
	if p := os.Getenv("HERDR_MOBILE_CONFIG"); p != "" {
		candidates = []string{p}
	} else {
		dir := os.Getenv("XDG_CONFIG_HOME")
		if dir == "" {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, ".config")
		}
		// The menu-bar app's config first: the root one is a migration backup.
		candidates = []string{
			filepath.Join(dir, "herdr-mobile", "desktop", "config.json"),
			filepath.Join(dir, "herdr-mobile", "config.json"),
		}
	}
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", "", p, err
		}
		var cfg struct {
			Listen    string `json:"listen"`
			AuthToken string `json:"authToken"`
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			return "", "", p, fmt.Errorf("parse %s: %w", p, err)
		}
		return cfg.Listen, cfg.AuthToken, p, nil
	}
	return "", "", "", fmt.Errorf("no config at %s", strings.Join(candidates, " or "))
}

// listenURL turns a listen address into a URL a local client can reach.
func listenURL(listen string) string {
	if listen == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "http://" + listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

type client struct {
	conn connection
	http *http.Client
}

func newClient(conn connection) *client {
	return &client{conn: conn, http: &http.Client{Timeout: 3 * time.Minute}}
}

// apiError is a non-2xx answer from the Gateway.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string {
	hint := ""
	switch e.status {
	case http.StatusUnauthorized:
		hint = " (token rejected: check HC_TOKEN)"
	case http.StatusConflict:
		hint = " (the session is not in a state that allows this; run \"hc show ID\")"
	case http.StatusUnprocessableEntity:
		hint = " (this agent does not support it; use \"hc term\" / \"hc keys\")"
	}
	return fmt.Sprintf("gateway: %d %s%s", e.status, e.message, hint)
}

func (c *client) do(ctx context.Context, method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.conn.baseURL+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.send(req, out)
}

func (c *client) send(req *http.Request, out any) error {
	req.Header.Set("Authorization", "Bearer "+c.conn.token)
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("gateway unreachable at %s: %w", c.conn.baseURL, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(b))
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		return &apiError{status: res.StatusCode, message: msg}
	}
	if out == nil || len(b) == 0 {
		return nil
	}
	if raw, ok := out.(*json.RawMessage); ok {
		*raw = append((*raw)[:0], b...)
		return nil
	}
	return json.Unmarshal(b, out)
}

func (c *client) sessions(ctx context.Context, archived bool) ([]model.Session, error) {
	path := "/v1/sessions"
	if archived {
		path += "?archived=true"
	}
	var res struct {
		Sessions []model.Session `json:"sessions"`
	}
	err := c.do(ctx, http.MethodGet, path, nil, &res)
	return res.Sessions, err
}

func (c *client) session(ctx context.Context, id string) (model.Session, error) {
	var s model.Session
	err := c.do(ctx, http.MethodGet, "/v1/sessions/"+url.PathEscape(id), nil, &s)
	return s, err
}

func (c *client) messages(ctx context.Context, id string) (model.Session, []model.Message, error) {
	var res struct {
		Session  model.Session   `json:"session"`
		Messages []model.Message `json:"messages"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/sessions/"+url.PathEscape(id)+"/messages", nil, &res)
	return res.Session, res.Messages, err
}

// upload sends a local file and returns its upload id.
func (c *client) upload(ctx context.Context, path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.conn.baseURL+"/v1/uploads", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	ct := mime.TypeByExtension(filepath.Ext(path))
	if ct == "" {
		ct = http.DetectContentType(b)
	}
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(path)}))
	var res struct {
		ID string `json:"id"`
	}
	if err := c.send(req, &res); err != nil {
		return "", fmt.Errorf("upload %s: %w", path, err)
	}
	return res.ID, nil
}
