package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
	"github.com/tohutohu/herdr-android-client/gateway/internal/providers/dsh"
)

func dshConfig(cfg config.Config) dsh.Config {
	models := make([]dsh.ModelRoute, 0, len(cfg.DSHModels))
	for _, m := range cfg.DSHModels {
		models = append(models, dsh.ModelRoute{ID: m.ID, Name: m.Name, Provider: m.Provider, Model: m.Model, Efforts: m.Efforts})
	}
	return dsh.Config{Binary: cfg.DSHBinary, Home: cfg.DSHHome, Model: cfg.DSHModel, APIKey: cfg.DSHAPIKey, Models: models, ProviderKeys: cfg.DSHProviderKeys}
}

// dshStatus is what the menu-bar app shows. It never contains a key.
type dshStatus struct {
	// Binary is the dsh the Gateway would run; Configured says it is set in
	// the config rather than found automatically.
	Binary     string `json:"binary,omitempty"`
	Configured bool   `json:"configured"`
	Version    string `json:"version,omitempty"`
	Error      string `json:"error,omitempty"`
	// Keys lists routes that have an API key, including deepseek-official.
	Keys         []string          `json:"keys"`
	Models       []config.DSHModel `json:"models"`
	DefaultModel string            `json:"defaultModel"`
}

// dshChange is read from stdin by "dsh configure". Absent fields are kept.
type dshChange struct {
	// Binary "" returns to automatic discovery.
	Binary *string `json:"binary"`
	// Keys by route; "" removes one. deepseek-official is dshApiKey.
	Keys   map[string]string  `json:"keys"`
	Models *[]config.DSHModel `json:"models"`
	// DefaultModel "" restores the built-in default.
	DefaultModel *string `json:"defaultModel"`
}

func dshCmd(args []string) error {
	if len(args) != 1 || args[0] != "status" && args[0] != "configure" {
		return errors.New("usage: dsh status | dsh configure < change.json")
	}
	store, err := config.Load(config.DefaultPath())
	if err != nil {
		return err
	}
	if args[0] == "configure" {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
		if err != nil {
			return err
		}
		var change dshChange
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&change); err != nil {
			return fmt.Errorf("read DSH settings: %w", err)
		}
		next, err := applyDSHChange(store.Get(), change)
		if err != nil {
			return err
		}
		if err := store.Update(func(c *config.Config) {
			c.DSHBinary, c.DSHAPIKey, c.DSHProviderKeys = next.DSHBinary, next.DSHAPIKey, next.DSHProviderKeys
			c.DSHModels, c.DSHModel = next.DSHModels, next.DSHModel
		}); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(statusOf(store.Get()), "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

// applyDSHChange returns cfg with the change, refusing anything that would
// stop the Gateway from starting.
func applyDSHChange(cfg config.Config, change dshChange) (config.Config, error) {
	if change.Binary != nil {
		p := strings.TrimSpace(*change.Binary)
		if p != "" {
			if !filepath.IsAbs(p) {
				return cfg, errors.New("the dsh path must be absolute")
			}
			if st, err := os.Stat(p); err != nil || st.IsDir() || st.Mode()&0111 == 0 {
				return cfg, fmt.Errorf("%s is not an executable file", p)
			}
		}
		cfg.DSHBinary = p
	}
	if len(change.Keys) > 0 {
		keys := map[string]string{}
		for route, key := range cfg.DSHProviderKeys {
			keys[route] = key
		}
		for route, key := range change.Keys {
			key = strings.TrimSpace(key)
			if len(key) > 4096 {
				return cfg, errors.New("API key is too long")
			}
			switch {
			case route == dsh.DeepSeekRoute:
				cfg.DSHAPIKey = key
			case key == "":
				delete(keys, route)
			default:
				keys[route] = key
			}
		}
		cfg.DSHProviderKeys = keys
		if len(keys) == 0 {
			cfg.DSHProviderKeys = nil
		}
	}
	if change.Models != nil {
		cfg.DSHModels = *change.Models
	}
	if change.DefaultModel != nil {
		cfg.DSHModel = strings.TrimSpace(*change.DefaultModel)
	}
	if err := dsh.Validate(dshConfig(cfg)); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func statusOf(cfg config.Config) dshStatus {
	s := dshStatus{Configured: cfg.DSHBinary != "", Keys: []string{}, Models: cfg.DSHModels, DefaultModel: cfg.DSHModel}
	if s.Models == nil {
		s.Models = []config.DSHModel{}
	}
	if s.DefaultModel == "" {
		s.DefaultModel = dsh.DefaultModel
	}
	if cfg.DSHAPIKey != "" {
		s.Keys = append(s.Keys, dsh.DeepSeekRoute)
	}
	for route := range cfg.DSHProviderKeys {
		s.Keys = append(s.Keys, route)
	}
	sort.Strings(s.Keys)
	if s.Binary = dsh.FindBinary(cfg.DSHBinary); s.Binary == "" {
		s.Error = "dsh was not found. Install it with: npm install --global @deepseek-ai/dsh@0.2.2-rc.1"
		return s
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.Binary, "--version")
	cmd.Env = append(os.Environ(), "PATH="+dsh.ChildPath(s.Binary, os.Getenv("PATH")))
	out, err := cmd.Output()
	if err != nil {
		detail := err.Error()
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			detail, _, _ = strings.Cut(strings.TrimSpace(string(exit.Stderr)), "\n")
		}
		s.Error = fmt.Sprintf("%s does not run: %s", s.Binary, detail)
		return s
	}
	s.Version = strings.TrimSpace(string(out))
	return s
}
