// Package config handles the gateway's only persistent state: settings,
// the auth token and registered FCM device tokens.
package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/providers/opencode"
)

type Device struct {
	Name         string    `json:"name"`
	FCMToken     string    `json:"fcmToken"`
	RegisteredAt time.Time `json:"registeredAt"`
}

type Config struct {
	FirebaseAndroid *FirebaseAndroid `json:"firebaseAndroid,omitempty"`
	// Optional TypeSafe key for advisory directory checks; TYPESAFE_API_KEY overrides it.
	JevAPIKey string `json:"jevApiKey,omitempty"`

	Listen    string   `json:"listen"`
	AuthToken string   `json:"authToken"`
	Devices   []Device `json:"devices"`

	// Firebase service account JSON path (can be overridden by
	// HERDR_MOBILE_FCM_CREDENTIALS or GOOGLE_APPLICATION_CREDENTIALS).
	FCMCredentialsFile string `json:"fcmCredentialsFile,omitempty"`

	HerdrSocket     string          `json:"herdrSocket,omitempty"`
	ClaudeConfigDir string          `json:"claudeConfigDir,omitempty"`
	OpenCode        opencode.Config `json:"opencode,omitempty"`
	CodexBinary     string          `json:"codexBinary,omitempty"`
	CodexDaemonSock string          `json:"codexDaemonSocket,omitempty"`
	UploadDir       string          `json:"uploadDir,omitempty"`
	// Directories under which the app may browse, create folders and start sessions.
	WorkspaceRoots []string `json:"workspaceRoots,omitempty"`
	// Sessions not running in Herdr are listed as offline when updated within this window.
	OfflineSessionDays int `json:"offlineSessionDays,omitempty"`
	// Command that reports subscription limits (CodexBar's CLI). "off" disables it.
	UsageCommand string `json:"usageCommand,omitempty"`
	// How often the limits are re-read in the background (default 5).
	UsageRefreshMinutes int `json:"usageRefreshMinutes,omitempty"`
}

// FirebaseAndroid is client configuration, never a service-account private key.
type FirebaseAndroid struct {
	APIKey        string `json:"apiKey"`
	ApplicationID string `json:"applicationId"`
	ProjectID     string `json:"projectId"`
	SenderID      string `json:"senderId"`
}

// GatewayID identifies notification provenance without revealing the bearer token.
func GatewayID(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// Store serialises access to the config file.
type Store struct {
	path string
	mu   sync.Mutex
	cfg  Config
}

func DefaultPath() string {
	if p := os.Getenv("HERDR_MOBILE_CONFIG"); p != "" {
		return p
	}
	// Use ~/.config (like herdr) rather than macOS's ~/Library/Application Support.
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "herdr-mobile", "config.json")
}

func StateDir() string {
	if p := os.Getenv("HERDR_MOBILE_STATE_DIR"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "herdr-mobile")
}

// Load reads the config, creating it with a fresh token when missing.
func Load(path string) (*Store, error) {
	s := &Store{path: path}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &s.cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	changed := s.applyDefaults()
	if changed || errors.Is(err, os.ErrNotExist) {
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) applyDefaults() bool {
	changed := false
	if s.cfg.AuthToken == "" {
		s.cfg.AuthToken = NewToken()
		changed = true
	}
	if s.cfg.Listen == "" {
		s.cfg.Listen = ":8765"
		changed = true
	}
	if s.cfg.OfflineSessionDays == 0 {
		s.cfg.OfflineSessionDays = 3
		changed = true
	}
	return changed
}

func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func (s *Store) Path() string { return s.path }

func (s *Store) Get() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cfg
	c.Devices = append([]Device(nil), s.cfg.Devices...)
	return c
}

func (s *Store) Update(fn func(*Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.cfg)
	return s.saveLocked()
}

// UpsertDevice registers a token, replacing an entry with the same name or token.
func (s *Store) UpsertDevice(name, token string) error {
	return s.Update(func(c *Config) {
		out := c.Devices[:0]
		for _, d := range c.Devices {
			if d.FCMToken != token && d.Name != name {
				out = append(out, d)
			}
		}
		c.Devices = append(out, Device{Name: name, FCMToken: token, RegisteredAt: time.Now().UTC()})
	})
}

func (s *Store) RemoveDeviceToken(token string) error {
	return s.Update(func(c *Config) {
		out := c.Devices[:0]
		for _, d := range c.Devices {
			if d.FCMToken != token {
				out = append(out, d)
			}
		}
		c.Devices = out
	})
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
