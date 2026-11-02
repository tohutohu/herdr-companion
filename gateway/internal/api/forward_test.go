package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
)

func TestGatewayはlocalhost宛のプロキシ要求をトークン付きで中継する(t *testing.T) {
	store, err := config.Load(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("dev server " + r.URL.Path))
	}))
	defer backend.Close()
	gateway := httptest.NewServer((&Server{Config: store}).Handler())
	defer gateway.Close()

	get := func(user *url.Userinfo) (int, string) {
		proxy, _ := url.Parse(gateway.URL)
		proxy.User = user
		res, err := (&http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}).Get(backend.URL + "/app")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(body)
	}
	if code, _ := get(nil); code != http.StatusProxyAuthRequired {
		t.Fatalf("without token: %d", code)
	}
	if code, body := get(url.UserPassword("herdr", store.Get().AuthToken)); code != 200 || body != "dev server /app" {
		t.Fatalf("with token: %d %q", code, body)
	}
	// The API paths are unchanged.
	res, err := http.Get(gateway.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("healthz %d", res.StatusCode)
	}
}
