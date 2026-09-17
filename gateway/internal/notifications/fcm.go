// Package notifications watches Herdr for session state changes and sends
// Firebase Cloud Messaging (HTTP v1) data messages.
package notifications

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

// ErrTokenInvalid means the device token should be forgotten.
var ErrTokenInvalid = errors.New("fcm token is no longer valid")

type serviceAccount struct {
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// FCM sends data messages with a service account. Access tokens are cached in
// memory only.
type FCM struct {
	account serviceAccount
	key     *rsa.PrivateKey
	http    *http.Client
	baseURL string

	mu      sync.Mutex
	token   string
	expires time.Time
}

// CredentialsPath resolves the service account file from env or config.
func CredentialsPath(configured string) string {
	for _, p := range []string{os.Getenv("HERDR_MOBILE_FCM_CREDENTIALS"), configured, os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")} {
		if p != "" {
			return p
		}
	}
	return ""
}

func NewFCM(credentialsFile string) (*FCM, error) {
	b, err := os.ReadFile(credentialsFile)
	if err != nil {
		return nil, err
	}
	var sa serviceAccount
	if err := json.Unmarshal(b, &sa); err != nil {
		return nil, fmt.Errorf("parse service account: %w", err)
	}
	if p := os.Getenv("HERDR_MOBILE_FCM_PROJECT_ID"); p != "" {
		sa.ProjectID = p
	}
	if sa.ProjectID == "" || sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, errors.New("service account JSON must contain project_id, client_email and private_key")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return nil, errors.New("service account private_key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		if k, err2 := x509.ParsePKCS1PrivateKey(block.Bytes); err2 == nil {
			parsed = k
		} else {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("service account key is not RSA")
	}
	return &FCM{account: sa, key: key, http: &http.Client{Timeout: 15 * time.Second}, baseURL: "https://fcm.googleapis.com"}, nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (f *FCM) accessToken(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.token != "" && time.Now().Before(f.expires.Add(-time.Minute)) {
		return f.token, nil
	}
	now := time.Now()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"iss":   f.account.ClientEmail,
		"scope": fcmScope,
		"aud":   f.account.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	signing := b64(header) + "." + b64(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(nil, f.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {signing + "." + b64(sig)},
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", f.account.TokenURI, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := f.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oauth token: %s: %s", resp.Status, body)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", err
	}
	f.token, f.expires = tok.AccessToken, now.Add(time.Duration(tok.ExpiresIn)*time.Second)
	return f.token, nil
}

// Send delivers a high-priority data-only message; the app builds the
// notification itself so it can also prefetch the session.
func (f *FCM) Send(ctx context.Context, deviceToken string, data map[string]string) error {
	access, err := f.accessToken(ctx)
	if err != nil {
		return err
	}
	msg := map[string]any{"message": map[string]any{
		"token": deviceToken,
		"data":  data,
		"android": map[string]any{
			"priority":    "HIGH",
			"ttl":         "86400s",
			"collapseKey": data["sessionId"],
		},
	}}
	body, _ := json.Marshal(msg)
	endpoint := fmt.Sprintf("%s/v1/projects/%s/messages:send", f.baseURL, url.PathEscape(f.account.ProjectID))
	req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode == http.StatusNotFound || bytes.Contains(respBody, []byte("UNREGISTERED")) {
		return fmt.Errorf("%w: %s", ErrTokenInvalid, respBody)
	}
	return fmt.Errorf("fcm send: %s: %s", resp.Status, respBody)
}
