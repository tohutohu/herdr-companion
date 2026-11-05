package translation

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Fixed upstream binaries and model revisions make trust_remote_code auditable.
const uvURL = "https://github.com/astral-sh/uv/releases/download/0.13.7/uv-aarch64-apple-darwin.tar.gz"
const uvSHA256 = "81b811b57e4624df7ed3aac148e0359100d9962f3d9dce00aec533dc2e086731"

var revisions = map[string]string{
	"0.8b": "e5fe5b7f26ebaeae48db0ce644b932d1d9b91aaa",
	"2b":   "63777ddc6dc80aacacd92a1bf6966105f2f6270c",
	"8b":   "9d87684408bdf34500b7d6115c257d83350c1fe5",
}

func (s *Service) install(ctx context.Context, size string) error {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	uv := filepath.Join(s.dir, "uv")
	if _, err := os.Stat(uv); err != nil {
		s.stage("実行環境をダウンロード中…")
		if err := downloadUV(ctx, uv); err != nil {
			return err
		}
	}
	worker := filepath.Join(s.dir, "worker.py")
	if err := os.WriteFile(worker, workerSource, 0600); err != nil {
		return err
	}
	venv := filepath.Join(s.dir, "venv")
	s.stage("Python・MLXを準備中…")
	if _, err := os.Stat(filepath.Join(venv, "bin", "python")); err != nil {
		if err := s.command(ctx, uv, "venv", "--python", "3.12", "--managed-python", venv); err != nil {
			return err
		}
	}
	// A marker is written only after pip succeeds. Failed setup is retryable.
	marker := filepath.Join(s.dir, "runtime-v1")
	if _, err := os.Stat(marker); err != nil {
		if err := s.command(ctx, uv, "pip", "install", "--python", filepath.Join(venv, "bin", "python"), "mlx-lm==0.32.2", "mlx==0.32.4", "transformers==5.22.1", "numba==0.68.1"); err != nil {
			return err
		}
		if err := os.WriteFile(marker, []byte("ok"), 0600); err != nil {
			return err
		}
	}
	s.stage("モデル " + size + " をダウンロード中…（初回のみ）")
	modelDir := s.modelDir(size)
	marker = filepath.Join(modelDir, ".herdr-ready")
	if data, _ := os.ReadFile(marker); string(data) != revisions[size] {
		if err := s.command(ctx, filepath.Join(venv, "bin", "python"), worker, "download", modelDir, "pfnet/plamo-3-translate-"+size+"-MLX-4bit", revisions[size]); err != nil {
			return err
		}
		return os.WriteFile(marker, []byte(revisions[size]), 0600)
	}
	return nil
}

func (s *Service) modelDir(size string) string { return filepath.Join(s.dir, "models", size) }
func (s *Service) command(ctx context.Context, binary string, args ...string) error {
	log, err := os.OpenFile(filepath.Join(s.dir, "setup.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := processContext(ctx, binary, args...)
	cmd.Env = append(s.environment(), "HF_HUB_OFFLINE=0")
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("セットアップに失敗: %w（translation/setup.log）", err)
	}
	return nil
}

func downloadUV(ctx context.Context, dest string) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", uvURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("uv download: HTTP %d", resp.StatusCode)
	}
	archive := dest + ".tar.gz"
	f, err := os.OpenFile(archive, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(archive)
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, hash), io.LimitReader(resp.Body, 100<<20))
	f.Close()
	if err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != uvSHA256 {
		return fmt.Errorf("uv checksum mismatch")
	}
	f, err = os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			return fmt.Errorf("uv archive: %w", err)
		}
		if h.Name != "uv-aarch64-apple-darwin/uv" {
			continue
		}
		out, err := os.OpenFile(dest+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0700)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, tr)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		return os.Rename(dest+".tmp", dest)
	}
}
