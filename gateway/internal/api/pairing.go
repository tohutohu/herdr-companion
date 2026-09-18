package api

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/tohutohu/herdr-android-client/gateway/internal/config"
)

// One outstanding invitation per gateway. Restart, cancellation, a new invitation,
// successful redemption or token rotation invalidates it. Secrets never enter URLs/logs.
type pairingState struct {
	mu        sync.Mutex
	code      string
	authToken string
	expires   time.Time
}

func (s *Server) createPairing(w http.ResponseWriter, r *http.Request) {
	s.pairing.mu.Lock()
	defer s.pairing.mu.Unlock()
	s.pairing.code = config.NewToken()
	s.pairing.authToken = s.Config.Get().AuthToken
	s.pairing.expires = time.Now().Add(5 * time.Minute)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"code": s.pairing.code, "expiresAt": s.pairing.expires})
}

func (s *Server) cancelPairing(w http.ResponseWriter, r *http.Request) {
	s.pairing.mu.Lock()
	s.pairing.code = ""
	s.pairing.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) redeemPairing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid pairing request")
		return
	}
	s.pairing.mu.Lock()
	defer s.pairing.mu.Unlock()
	cfg := s.Config.Get()
	if s.pairing.code == "" || time.Now().After(s.pairing.expires) || cfg.AuthToken != s.pairing.authToken ||
		subtle.ConstantTimeCompare([]byte(req.Code), []byte(s.pairing.code)) != 1 {
		writeError(w, http.StatusUnauthorized, "pairing code expired or invalid; create a new QR code on the Mac")
		return
	}
	s.pairing.code = ""
	// Explicit allowlist: no config serialization, credentials path or private key.
	writeJSON(w, http.StatusOK, struct {
		Token     string                  `json:"token"`
		GatewayID string                  `json:"gatewayId"`
		Firebase  *config.FirebaseAndroid `json:"firebase,omitempty"`
	}{cfg.AuthToken, config.GatewayID(cfg.AuthToken), cfg.FirebaseAndroid})
}
