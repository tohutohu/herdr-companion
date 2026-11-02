package forward

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const token = "secret-token"

func proxyServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(&Proxy{Token: func() string { return token }})
	t.Cleanup(srv.Close)
	return srv
}

func clientVia(t *testing.T, proxy *httptest.Server, user *url.Userinfo) *http.Client {
	t.Helper()
	u, _ := url.Parse(proxy.URL)
	u.User = user
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u)}}
}

func TestLocalhostへのリクエストをそのまま中継する(t *testing.T) {
	var gotAuth, gotHost string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Proxy-Authorization")
		gotHost = r.Host
		body, _ := io.ReadAll(r.Body)
		w.Write([]byte(r.Method + " " + r.URL.RequestURI() + " " + string(body)))
	}))
	defer backend.Close()
	proxy := proxyServer(t)
	client := clientVia(t, proxy, url.UserPassword("herdr", token))

	res, err := client.Post(backend.URL+"/api/items?x=1", "text/plain", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || string(body) != "POST /api/items?x=1 hello" {
		t.Fatalf("status %d body %q", res.StatusCode, body)
	}
	if gotAuth != "" {
		t.Fatalf("Proxy-Authorization reached the server: %q", gotAuth)
	}
	if gotHost != strings.TrimPrefix(backend.URL, "http://") {
		t.Fatalf("Host = %q", gotHost)
	}
}

func TestトークンがないとProxy認証を求める(t *testing.T) {
	proxy := proxyServer(t)
	for _, user := range []*url.Userinfo{nil, url.UserPassword("herdr", "wrong")} {
		res, err := clientVia(t, proxy, user).Get("http://localhost:1/")
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusProxyAuthRequired || !strings.Contains(res.Header.Get("Proxy-Authenticate"), "Basic") {
			t.Fatalf("user %v: status %d, Proxy-Authenticate %q", user, res.StatusCode, res.Header.Get("Proxy-Authenticate"))
		}
	}
}

func TestLocalhost以外へは中継しない(t *testing.T) {
	proxy := proxyServer(t)
	res, err := clientVia(t, proxy, url.UserPassword("herdr", token)).Get("http://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func Test待ち受けていないポートは502にする(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	proxy := proxyServer(t)
	res, err := clientVia(t, proxy, url.UserPassword("herdr", token)).Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestCONNECTはlocalhostへのトンネルになる(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		c, err := echo.Accept()
		if err != nil {
			return
		}
		io.Copy(c, c)
		c.Close()
	}()
	proxy := proxyServer(t)
	conn, err := net.Dial("tcp", strings.TrimPrefix(proxy.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	target := echo.Addr().String()
	io.WriteString(conn, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\nProxy-Authorization: Bearer "+token+"\r\n\r\n")
	br := bufio.NewReader(conn)
	res, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	io.WriteString(conn, "ping")
	got := make([]byte, 4)
	if _, err := io.ReadFull(br, got); err != nil || string(got) != "ping" {
		t.Fatalf("echo %q, %v", got, err)
	}
}

func TestCONNECTもlocalhost以外は断る(t *testing.T) {
	proxy := proxyServer(t)
	conn, err := net.Dial("tcp", strings.TrimPrefix(proxy.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\nProxy-Authorization: Bearer "+token+"\r\n\r\n")
	res, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func Test転送先はループバックの明示ポートに限る(t *testing.T) {
	ok := []string{"localhost:3000", "LOCALHOST:5173", "127.0.0.1:8080", "127.1.2.3:80", "[::1]:3000"}
	for _, h := range ok {
		if _, err := Target(h); err != nil {
			t.Errorf("%s: %v", h, err)
		}
	}
	ng := []string{"example.com:80", "100.99.15.34:8765", "0.0.0.0:3000", "localhost", "localhost:0", "localhost:70000", "localhost.evil.com:80", "[::]:3000"}
	for _, h := range ng {
		if _, err := Target(h); err == nil {
			t.Errorf("%s accepted", h)
		}
	}
}

func Testプロキシ要求とAPI要求を見分ける(t *testing.T) {
	api := httptest.NewRequest("GET", "/v1/sessions", nil)
	abs := httptest.NewRequest("GET", "http://localhost:3000/", nil)
	connect := httptest.NewRequest("CONNECT", "localhost:3000", nil)
	if IsProxyRequest(api) || !IsProxyRequest(abs) || !IsProxyRequest(connect) {
		t.Fatalf("api %v abs %v connect %v", IsProxyRequest(api), IsProxyRequest(abs), IsProxyRequest(connect))
	}
}

func Test直接つなげるプロキシのアドレスを知らせる(t *testing.T) {
	direct := httptest.NewRequest("GET", "http://192.168.1.20:8766/v1/capabilities", nil)
	tunnel := httptest.NewRequest("GET", "http://gateway.example.com/v1/capabilities", nil)
	tunnel.Header.Set("Cf-Connecting-Ip", "203.0.113.5")
	local := httptest.NewRequest("GET", "http://127.0.0.1:8766/v1/capabilities", nil)
	cases := []struct {
		listen string
		r      *http.Request
		want   string
	}{
		{"100.99.15.34:8765", tunnel, "http://100.99.15.34:8765"},
		{"100.99.15.34:8765", direct, "http://100.99.15.34:8765"},
		{":8766", direct, "http://192.168.1.20:8766"},
		{"0.0.0.0:8766", tunnel, ""},
		{"127.0.0.1:8766", local, ""},
	}
	for _, c := range cases {
		if got := DirectAddress(c.listen, c.r); got != c.want {
			t.Errorf("listen %q host %q: got %q, want %q", c.listen, c.r.Host, got, c.want)
		}
	}
}
