// Package localauth implements pre-pairing operator authentication on
// the recorder.
//
// The recorder ships with no pre-paired identity to a Management Server.
// Without this package, a fresh install is unreachable from a LAN
// browser (mediamtx.yml's authInternalUsers gates /v1 to localhost
// only). The integrator workflow — download → install → open browser
// to LAN IP → log in → configure → pair to MS — needs a recorder-local
// LocalUser store, login endpoint, and JWT issuance independent of
// the MS.
//
// Architecture
//
// Two parallel JWT validation paths land at internal/auth.Manager:
//
//   - MS-issued JWTs: validated against the bound MS's published JWKS,
//     chain-pinned to the MS's root CA per ADR 0012 D5. Only active
//     when the recorder is paired (see core.applyPairingAwareAuth).
//
//   - Recorder-local JWTs: validated against this package's locally-
//     persisted ES256 signing key. Always active; produced by
//     POST /v1/recorder/login. Tokens carry the same
//     `mediamtx_permissions` claim shape as MS-issued JWTs so the
//     existing per-endpoint requirePermission middleware (slice 4-D)
//     gates them identically.
//
// Recorder-local tokens are accepted even after pairing so a local
// admin retains a break-glass login for diagnostics if the MS is
// unreachable. The recorder is the *issuer* for these tokens; their
// scope is the local admin's role-derived permission set (admin role
// for the bootstrap admin per ADR 0010 D4).
//
// On-disk layout (under <identityDir>):
//
//	recorder-local-jwt.key         — ECDSA P-256 private key (PEM, mode 0600)
//	recorder-local-jwt.kid         — current key id (text, mode 0644)
//	initial-admin-password.txt     — written ONCE on first bootstrap (mode 0600)
//	recorder.db                    — SQLite store (LocalUsers; see internal/store)
//
// AGENTS.md §10 ("New endpoints must default to authenticated access.
// Anonymous access is opt-in per path, not the default") is the design
// rule this package implements.
package localauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	signingKeyFile     = "recorder-local-jwt.key"
	signingKeyIDFile   = "recorder-local-jwt.kid"
	signingKeyMode     = 0o600
	signingKeyIDMode   = 0o644
	signingKeyValidity = 365 * 24 * time.Hour
)

// SigningKey is the recorder's locally-persisted JWT signing key. ES256
// (ECDSA P-256) matches the MS's signing curve so the same JWT
// machinery (golang-jwt/jwt/v5) handles both sides.
type SigningKey struct {
	KID  string
	Priv *ecdsa.PrivateKey
}

// LoadOrCreateSigningKey returns the persisted signing key from
// dir, generating + persisting a fresh one on first call. Idempotent.
func LoadOrCreateSigningKey(dir string) (*SigningKey, error) {
	if dir == "" {
		return nil, errors.New("localauth: empty dir")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("localauth: mkdir %q: %w", dir, err)
	}
	keyPath := filepath.Join(dir, signingKeyFile)
	kidPath := filepath.Join(dir, signingKeyIDFile)

	if data, err := os.ReadFile(keyPath); err == nil {
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, fmt.Errorf("localauth: decode %q: no PEM block", keyPath)
		}
		priv, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("localauth: parse %q: %w", keyPath, err)
		}
		kidBytes, err := os.ReadFile(kidPath)
		if err != nil {
			return nil, fmt.Errorf("localauth: read kid: %w", err)
		}
		kid := strings.TrimSpace(string(kidBytes))
		if kid == "" {
			return nil, fmt.Errorf("localauth: empty kid file")
		}
		return &SigningKey{KID: kid, Priv: priv}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("localauth: read key: %w", err)
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("localauth: generate key: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("localauth: marshal key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := writeFileAtomic(keyPath, keyPEM, signingKeyMode); err != nil {
		return nil, fmt.Errorf("localauth: write key: %w", err)
	}
	kid := "recorder-local-" + time.Now().UTC().Format("20060102")
	if err := writeFileAtomic(kidPath, []byte(kid+"\n"), signingKeyIDMode); err != nil {
		return nil, fmt.Errorf("localauth: write kid: %w", err)
	}
	return &SigningKey{KID: kid, Priv: priv}, nil
}

// PublicKey returns the public key half of the signing key. Surfaced
// for clients that want to verify recorder-local JWTs out of band.
func (s *SigningKey) PublicKey() *ecdsa.PublicKey {
	return &s.Priv.PublicKey
}

// writeFileAtomic writes data to path via a temp file + rename so
// readers never observe a partial write. Mirrors identity.writeFileAtomic.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
