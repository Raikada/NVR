// Package mediasign issues and verifies short-TTL HMAC signatures for
// media URLs (SP4): snapshots and clips are fetched by <img> tags and
// webhook consumers that can't send Authorization headers, so the URL
// itself carries a expiring signature.
package mediasign

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const keyFile = "media-sign.key"

// Signer signs and verifies media paths with an identity-dir-persisted
// HMAC key.
type Signer struct {
	key []byte
	now func() time.Time
}

// Open loads (or creates, mode 0600) the signing key under identityDir.
func Open(identityDir string) (*Signer, error) {
	path := filepath.Join(identityDir, keyFile)
	key, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		key = make([]byte, 32)
		if _, rerr := rand.Read(key); rerr != nil {
			return nil, rerr
		}
		if werr := os.WriteFile(path, key, 0o600); werr != nil {
			return nil, werr
		}
	} else if err != nil {
		return nil, err
	}
	if len(key) < 32 {
		return nil, fmt.Errorf("media sign key at %s is too short (%d bytes)", path, len(key))
	}
	return &Signer{key: key, now: time.Now}, nil
}

// Sign returns the unix expiry + hex signature for a path.
func (s *Signer) Sign(path string, ttl time.Duration) (exp int64, sig string) {
	exp = s.now().Add(ttl).Unix()
	return exp, s.compute(path, exp)
}

// Verify reports whether sig matches path+exp and exp is in the future.
func (s *Signer) Verify(path string, exp int64, sig string) bool {
	if s.now().Unix() >= exp {
		return false
	}
	want := s.compute(path, exp)
	return hmac.Equal([]byte(want), []byte(sig))
}

func (s *Signer) compute(path string, exp int64) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(path))
	mac.Write([]byte{0})
	mac.Write([]byte(strconv.FormatInt(exp, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}
