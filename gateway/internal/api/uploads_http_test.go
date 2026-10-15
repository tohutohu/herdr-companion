package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/uploads"
)

type generatedUpload struct{}

func (generatedUpload) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestHTTPで500MiBの添付を全量保存できる(t *testing.T) {
	var store *uploads.Store
	ts, _, _, token := newTestServer(t, func(s *Server) { store = s.Uploads })
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/uploads", io.LimitReader(generatedUpload{}, uploads.MaxUploadSize))
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = uploads.MaxUploadSize
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var result struct{ ID string }
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	f, err := store.Get(result.ID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(f.Path)
	if err != nil || info.Size() != uploads.MaxUploadSize {
		t.Fatalf("stored size = %v, error = %v", info, err)
	}
}

func Test既知の上限超過サイズはHTTPで413を返す(t *testing.T) {
	ts, _, _, token := newTestServer(t)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/uploads", io.LimitReader(generatedUpload{}, uploads.MaxUploadSize+1))
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = uploads.MaxUploadSize + 1
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
