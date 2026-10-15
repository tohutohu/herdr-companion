package uploads

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func Test保存したファイルは名前と種別ごと取り出せる(t *testing.T) {
	s := New(t.TempDir(), time.Hour)
	for _, tc := range []struct {
		name     string
		given    string
		ctype    string
		wantName string
		wantMime string
	}{
		{"画像", "shot.PNG", "image/png", "shot.PNG", "image/png"},
		{"名前なしはContent-Typeから決める", "", "image/jpeg", "", "image/jpeg"},
		{"未知の種別はoctet-stream", "", "application/x-thing", "", "application/octet-stream"},
		{"空白はアンダースコアになる", "売上 レポート.csv", "text/csv", "売上_レポート.csv", "text/csv"},
		{"パス区切りと先頭のドットは落とす", "../../etc/passwd", "text/plain", "passwd", "text/plain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := s.Save(strings.NewReader("body"), tc.ctype, tc.given)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantName != "" && f.Name != tc.wantName {
				t.Errorf("name = %q, want %q", f.Name, tc.wantName)
			}
			got, err := s.Get(f.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != f.Name || got.Path != f.Path {
				t.Errorf("get = %+v, want %+v", got, f)
			}
			if got.Mime != tc.wantMime {
				t.Errorf("mime = %q, want %q", got.Mime, tc.wantMime)
			}
			if got.IsImage() != strings.HasPrefix(tc.wantMime, "image/") {
				t.Errorf("IsImage = %v for %q", got.IsImage(), got.Mime)
			}
		})
	}
}

func Test大きすぎるアップロードは保存しない(t *testing.T) {
	s := New(t.TempDir(), time.Hour)
	body := io.LimitReader(zeroReader{}, MaxUploadSize+1)
	if _, err := s.Save(body, "text/plain", "big.txt"); err != ErrTooLarge {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}
	if _, err := s.Get("0123456789abcdef0123456789abcdef"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	if _, err := s.Get("../../etc"); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	entries, err := os.ReadDir(s.Dir())
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected upload left files: %v, %v", entries, err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func Test上限ちょうどの500MiBをストリーム保存できる(t *testing.T) {
	s := New(t.TempDir(), time.Hour)
	f, err := s.Save(io.LimitReader(zeroReader{}, MaxUploadSize), "application/octet-stream", "large.bin")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(f.Path)
	if err != nil || info.Size() != MaxUploadSize {
		t.Fatalf("stored size = %v, error = %v", info, err)
	}
}

type interruptedReader struct {
	sent bool
}

func (r *interruptedReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, io.ErrUnexpectedEOF
	}
	r.sent = true
	return copy(p, "partial"), nil
}

func Test転送中断時は不完全なファイルを残さない(t *testing.T) {
	s := New(t.TempDir(), time.Hour)
	if _, err := s.Save(&interruptedReader{}, "text/plain", "partial.txt"); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("error = %v", err)
	}
	entries, err := os.ReadDir(s.Dir())
	if err != nil || len(entries) != 0 {
		t.Fatalf("interrupted upload left files: %v, %v", entries, err)
	}
}

func Test期限切れのアップロードは削除される(t *testing.T) {
	s := New(t.TempDir(), 0)
	f, err := s.Save(strings.NewReader("body"), "text/plain", "notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	s.Cleanup()
	if _, err := s.Get(f.ID); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
