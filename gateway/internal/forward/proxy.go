// Package forward lets the app reach web servers listening on this Mac's
// loopback, such as a dev server an agent started: the Gateway acts as an HTTP
// proxy for localhost only, so the page keeps its own origin
// (http://localhost:3000) and its scripts, forms and WebSockets work as they
// would in a browser on the Mac.
package forward

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Proxy answers proxy requests (absolute-form URLs and CONNECT) whose target is
// a loopback host. Token returns the Gateway's auth token, which the client
// sends as the password of Proxy-Authorization: Basic (or as a Bearer token).
type Proxy struct {
	Token func() string

	dialer      net.Dialer
	reverseOnce sync.Once
	reverse     *httputil.ReverseProxy
}

// IsProxyRequest reports whether r asks the server to act as a proxy rather
// than to serve one of its own paths.
func IsProxyRequest(r *http.Request) bool {
	return r.Method == http.MethodConnect || r.URL.IsAbs()
}

// ErrNotLoopback is returned for a target outside this Mac's loopback.
var ErrNotLoopback = errors.New("only localhost can be forwarded")

// Target checks that hostport names a loopback host with an explicit port and
// returns it normalized for dialing.
func Target(hostport string) (string, error) {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return "", ErrNotLoopback
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", ErrNotLoopback
	}
	switch strings.ToLower(host) {
	case "localhost":
	default:
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", ErrNotLoopback
		}
	}
	return net.JoinHostPort(host, port), nil
}

func (p *Proxy) authorized(r *http.Request) bool {
	want := p.Token()
	if want == "" {
		return false
	}
	h := r.Header.Get("Proxy-Authorization")
	var got string
	if b, ok := strings.CutPrefix(h, "Bearer "); ok {
		got = b
	} else if b, ok := strings.CutPrefix(h, "Basic "); ok {
		raw, err := base64.StdEncoding.DecodeString(b)
		if err != nil {
			return false
		}
		_, got, _ = strings.Cut(string(raw), ":")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !p.authorized(r) {
		w.Header().Set("Proxy-Authenticate", `Basic realm="Herdr Companion"`)
		http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
		return
	}
	hostport := r.Host
	if r.Method != http.MethodConnect {
		hostport = r.URL.Host
		if r.URL.Port() == "" {
			port := "80"
			if r.URL.Scheme == "https" {
				port = "443"
			}
			hostport = net.JoinHostPort(r.URL.Hostname(), port)
		}
	}
	target, err := Target(hostport)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if r.Method == http.MethodConnect {
		p.tunnel(w, r, target)
		return
	}
	if r.URL.Scheme != "http" {
		http.Error(w, "use CONNECT for "+r.URL.Scheme, http.StatusBadRequest)
		return
	}
	p.reverseProxy().ServeHTTP(w, r)
}

func (p *Proxy) reverseProxy() *httputil.ReverseProxy {
	p.reverseOnce.Do(func() {
		p.reverse = &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.Out.URL = pr.In.URL
				pr.Out.Host = pr.In.URL.Host
				pr.Out.RequestURI = ""
				pr.Out.Header.Del("Proxy-Authorization")
				pr.Out.Header.Del("Proxy-Connection")
			},
			Transport: &http.Transport{
				// Never through another proxy: the target is this Mac.
				Proxy:                 nil,
				DialContext:           p.dialer.DialContext,
				MaxIdleConnsPerHost: 8,
				IdleConnTimeout:     90 * time.Second,
			},
			// Streamed responses (server-sent events, dev server reloads) go out as they come.
			FlushInterval: -1,
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				slog.Debug("forward request failed", "target", r.URL.Host, "error", err)
				http.Error(w, "nothing answered at "+r.URL.Host+" on the Mac", http.StatusBadGateway)
			},
		}
	})
	return p.reverse
}

// tunnel carries a CONNECT (HTTPS or a WebSocket through the proxy) as raw bytes.
func (p *Proxy) tunnel(w http.ResponseWriter, r *http.Request, target string) {
	upstream, err := p.dialer.DialContext(r.Context(), "tcp", target)
	if err != nil {
		http.Error(w, "nothing answered at "+target+" on the Mac", http.StatusBadGateway)
		return
	}
	conn, buf, err := http.NewResponseController(w).Hijack()
	if err != nil {
		upstream.Close()
		http.Error(w, "tunnel not supported", http.StatusInternalServerError)
		return
	}
	// The server's own read and write deadlines are for requests, not a tunnel.
	conn.SetDeadline(time.Time{})
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		conn.Close()
		upstream.Close()
		return
	}
	done := make(chan struct{}, 2)
	go func() {
		// Bytes the client sent right after CONNECT may already be buffered.
		io.Copy(upstream, io.MultiReader(io.LimitReader(buf, int64(buf.Reader.Buffered())), conn))
		closeWrite(upstream)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(conn, upstream)
		closeWrite(conn)
		done <- struct{}{}
	}()
	<-done
	<-done
	conn.Close()
	upstream.Close()
}

func closeWrite(c net.Conn) {
	if tc, ok := c.(interface{ CloseWrite() error }); ok {
		tc.CloseWrite()
	} else {
		c.Close()
	}
}
