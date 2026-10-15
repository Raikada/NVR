// Package cameracred implements an AES-GCM envelope around per-camera
// secrets (RTSP password, ONVIF password, webhook signing secret, SMTP
// password). The envelope key lives at <identityDir>/cred.key (mode 0600)
// and is generated on first Open. The vault is the only place plaintext
// secrets are decrypted; consumers (path manager, webhook dispatcher)
// receive materialized URLs / strings without ever touching ciphertext.
package cameracred

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const keyFileName = "cred.key"
const keySize = 32 // AES-256

// ErrShortKey is returned when an existing key file on disk is shorter
// than the AES-256 size. Callers should refuse to start.
var ErrShortKey = errors.New("cameracred: key file shorter than 32 bytes")

// Vault holds the AES-GCM AEAD plus its key for nonce generation.
type Vault struct {
	aead cipher.AEAD
}

// Open loads or creates the vault key under identityDir and returns a
// ready Vault. Idempotent: subsequent Opens with the same dir return a
// vault using the same key.
func Open(identityDir string) (*Vault, error) {
	if err := os.MkdirAll(identityDir, 0o700); err != nil {
		return nil, fmt.Errorf("cameracred: identity dir: %w", err)
	}
	keyPath := filepath.Join(identityDir, keyFileName)
	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("cameracred: aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cameracred: gcm: %w", err)
	}
	return &Vault{aead: aead}, nil
}

// Encrypt produces (ciphertext, nonce). Each call generates a fresh
// random nonce.
func (v *Vault) Encrypt(plain []byte) (ct, nonce []byte, err error) {
	nonce = make([]byte, v.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("cameracred: nonce: %w", err)
	}
	ct = v.aead.Seal(nil, nonce, plain, nil)
	return ct, nonce, nil
}

// Decrypt reverses Encrypt. Authentication failure produces an error.
func (v *Vault) Decrypt(ct, nonce []byte) ([]byte, error) {
	plain, err := v.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("cameracred: decrypt: %w", err)
	}
	return plain, nil
}

func loadOrCreateKey(path string) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		if len(data) < keySize {
			return nil, ErrShortKey
		}
		return data[:keySize], nil
	}
	key := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("cameracred: gen key: %w", err)
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("cameracred: persist key: %w", err)
	}
	return key, nil
}
