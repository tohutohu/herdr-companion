package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
)

func ptr[T any](v T) *T { return &v }

func TestDSH設定変更でキーを追加削除し公式キーはdshApiKeyに入れる(t *testing.T) {
	cfg := config.Config{DSHProviderKeys: map[string]string{"openrouter": "old"}}
	next, err := applyDSHChange(cfg, dshChange{Keys: map[string]string{"opencode-go": " go-key \n", "openrouter": "", "deepseek-official": "ds"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(next.DSHProviderKeys, map[string]string{"opencode-go": "go-key"}) || next.DSHAPIKey != "ds" {
		t.Fatalf("keys %v, deepseek %q", next.DSHProviderKeys, next.DSHAPIKey)
	}
	if cfg.DSHProviderKeys["openrouter"] != "old" {
		t.Fatal("the loaded config was modified")
	}
	next, err = applyDSHChange(next, dshChange{Keys: map[string]string{"opencode-go": ""}})
	if err != nil || next.DSHProviderKeys != nil {
		t.Fatalf("all keys removed: %v, %v", next.DSHProviderKeys, err)
	}
}

func TestDSH設定変更でモデルと既定モデルを保存する(t *testing.T) {
	models := []config.DSHModel{{ID: "go-kimi-k3", Name: "Kimi K3", Provider: "opencode-go", Model: "kimi-k3"}}
	next, err := applyDSHChange(config.Config{}, dshChange{Models: &models, DefaultModel: ptr("go-kimi-k3")})
	if err != nil {
		t.Fatal(err)
	}
	if next.DSHModel != "go-kimi-k3" || len(next.DSHModels) != 1 {
		t.Fatalf("%+v", next)
	}
	// Removing the models while the default still names one would stop the Gateway.
	if _, err := applyDSHChange(next, dshChange{Models: &[]config.DSHModel{}}); err == nil {
		t.Fatal("accepted a default model without a route")
	}
	next, err = applyDSHChange(next, dshChange{Models: &[]config.DSHModel{}, DefaultModel: ptr("")})
	if err != nil || next.DSHModel != "" || len(next.DSHModels) != 0 {
		t.Fatalf("%+v, %v", next, err)
	}
}

func TestDSH設定変更は実行できない実行ファイルを拒む(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "dsh")
	if err := os.WriteFile(plain, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"dsh", dir, plain, filepath.Join(dir, "missing")} {
		if _, err := applyDSHChange(config.Config{}, dshChange{Binary: ptr(p)}); err == nil {
			t.Errorf("%s: accepted", p)
		}
	}
	if err := os.Chmod(plain, 0700); err != nil {
		t.Fatal(err)
	}
	next, err := applyDSHChange(config.Config{}, dshChange{Binary: ptr(plain)})
	if err != nil || next.DSHBinary != plain {
		t.Fatalf("%q, %v", next.DSHBinary, err)
	}
	if next, err = applyDSHChange(next, dshChange{Binary: ptr("")}); err != nil || next.DSHBinary != "" {
		t.Fatalf("automatic: %q, %v", next.DSHBinary, err)
	}
}

func TestDSH状態にはキーの値を含めない(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	s := statusOf(config.Config{DSHAPIKey: "secret-ds", DSHProviderKeys: map[string]string{"opencode-go": "secret-go"}})
	if !reflect.DeepEqual(s.Keys, []string{"deepseek-official", "opencode-go"}) || s.Binary != "" || s.Error == "" || s.DefaultModel != "deepseek-flash" {
		t.Fatalf("%+v", s)
	}
}
