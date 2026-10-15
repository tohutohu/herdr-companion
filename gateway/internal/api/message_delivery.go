package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

// MessageDeliveryStore retains small, content-free receipts. A receipt is made
// durable BEFORE a provider is invoked; an interrupted send is never replayed.
// Receipts deliberately do not expire: expiring an unknown outcome would let a
// delayed retry execute the original side effect again.
type MessageDeliveryStore struct {
	dir string
	mu  sync.Mutex
}

type deliveryReceipt struct {
	Digest   string `json:"digest"`
	Accepted bool   `json:"accepted"`
}

var deliveryID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var errDeliveryUnknown = errors.New("Delivery unconfirmed. Check the conversation before sending again.")
var errDeliveryConflict = errors.New("This request ID was already used for a different message.")

func OpenMessageDeliveryStore(dir string) (*MessageDeliveryStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := syncDeliveryDirectory(filepath.Dir(dir)); err != nil {
		return nil, err
	}
	return &MessageDeliveryStore{dir: dir}, nil
}

func (s *MessageDeliveryStore) path(scope, requestID string) string {
	key := sha256.Sum256([]byte(scope + "\x00" + requestID))
	return filepath.Join(s.dir, hex.EncodeToString(key[:])+".json")
}

func (s *MessageDeliveryStore) previous(scope, requestID, digest string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path(scope, requestID))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return true, errDeliveryUnknown
	}
	var receipt deliveryReceipt
	if json.Unmarshal(b, &receipt) != nil {
		return true, errDeliveryUnknown
	}
	if receipt.Digest != digest {
		return true, errDeliveryConflict
	}
	if !receipt.Accepted {
		return true, errDeliveryUnknown
	}
	return true, nil
}

// claim returns true only for a request that has not yet reached a provider.
// Existing accepted requests return false,nil; all other receipts fail closed.
func (s *MessageDeliveryStore) claim(scope, requestID, digest string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.path(scope, requestID)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		b, err := os.ReadFile(path)
		if err != nil {
			return false, errDeliveryUnknown
		}
		var receipt deliveryReceipt
		if json.Unmarshal(b, &receipt) != nil {
			return false, errDeliveryUnknown
		}
		if receipt.Digest != digest {
			return false, errDeliveryConflict
		}
		if !receipt.Accepted {
			return false, errDeliveryUnknown
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(deliveryReceipt{Digest: digest}); err != nil {
		return false, err
	}
	if err := f.Sync(); err != nil {
		return false, err
	}
	if err := syncDeliveryDirectory(s.dir); err != nil {
		return false, err
	}
	return true, nil
}

func (s *MessageDeliveryStore) accept(scope, requestID, digest string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.CreateTemp(s.dir, ".receipt-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := json.NewEncoder(f).Encode(deliveryReceipt{Digest: digest, Accepted: true}); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), s.path(scope, requestID)); err != nil {
		return err
	}
	return syncDeliveryDirectory(s.dir)
}

func syncDeliveryDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
