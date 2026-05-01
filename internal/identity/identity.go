// Package identity manages the recorder's persistent device identity:
// the self-generated UUIDv7 (per ADR 0002 D3), the ECDSA P-256 mTLS
// keypair the recorder presents during pairing and on the recorder ↔
// MS WebSocket (per ADR 0011 D1), and the issued DeviceIdentity
// material received from the Management Server at pairing
// (per ADR 0012 D5: pinned root CA fingerprints + issued cert + chain).
//
// Storage lives in a single directory whose path is operator-
// configured (defaults to "<confDir>/identity"). The recorder
// generates the id and keypair on first start so an unpaired
// recorder still has a stable identity. The DeviceIdentity fields
// (issued cert, chain, pinned roots) are absent until the recorder
// has been paired with an MS.
//
// On-disk layout:
//
//	<dir>/
//	  id             — UUIDv7 string (text)
//	  recorder.key   — ECDSA P-256 private key (PEM, mode 0600)
//	  recorder.pub   — public key (PEM, mode 0644)
//	  device.crt     — issued mTLS cert from MS (PEM, mode 0644; absent pre-pair)
//	  chain.crt      — issuing chain back to MS root (PEM, mode 0644; absent pre-pair)
//	  pinned-roots.json — pinned root CA fingerprints + cert PEMs (mode 0644; absent pre-pair)
//
// Filesystem-permissions-as-protection is the v1 approach. Encrypted-
// with-hardware-bound-key per pairing-flows §2.3 step 12 is a real
// future requirement; TPM/keystore integration is its own ADR-level
// surface (see slice 3 phase D notes).
package identity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	idFile        = "id"
	keyFile       = "recorder.key"
	pubFile       = "recorder.pub"
	certFile      = "device.crt"
	chainFile     = "chain.crt"
	pinnedRoots   = "pinned-roots.json"
	keyFileMode   = 0o600
	pubFileMode   = 0o644
	dirMode       = 0o700
)

// PinnedRoot is one entry in the recorder's trust store of MS root
// CAs (per ADR 0012 D5).
type PinnedRoot struct {
	FingerprintSHA256 string    `json:"fingerprint_sha256"`
	CertPEM           string    `json:"cert_pem"`
	NotBefore         time.Time `json:"not_before"`
	NotAfter          time.Time `json:"not_after"`
	Kind              string    `json:"kind"` // "ms_self_signed" | "cloud_issued_intermediate"
	PinnedAt          time.Time `json:"pinned_at"`
}

// Identity is the recorder's persistent identity. Read-mostly: the id
// and keypair are set once at first install and never change for the
// life of the recorder (per ADR 0002 D3). The issued cert and pinned
// roots are populated at pairing and rotated thereafter per ADR 0011
// D4.
type Identity struct {
	dir string
	mu  sync.RWMutex

	id       uuid.UUID
	priv     *ecdsa.PrivateKey
	cert     []byte // PEM-encoded issued mTLS cert; nil if unpaired
	chain    []byte // PEM-encoded chain to MS root; nil if unpaired
	roots    []PinnedRoot
}

// Open opens or creates the recorder's identity at dir. On first call
// it generates the UUIDv7 and ECDSA P-256 keypair, persists them, and
// returns the populated Identity. Subsequent calls load the existing
// material. Idempotent.
func Open(dir string) (*Identity, error) {
	if dir == "" {
		return nil, errors.New("identity: empty dir")
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("identity: mkdir %q: %w", dir, err)
	}
	id := &Identity{dir: dir}
	if err := id.loadOrCreate(); err != nil {
		return nil, err
	}
	return id, nil
}

func (i *Identity) loadOrCreate() error {
	// id
	idPath := filepath.Join(i.dir, idFile)
	if data, err := os.ReadFile(idPath); err == nil {
		u, perr := uuid.Parse(string(stripTrailing(data)))
		if perr != nil {
			return fmt.Errorf("identity: parse %q: %w", idPath, perr)
		}
		i.id = u
	} else if errors.Is(err, os.ErrNotExist) {
		newID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("identity: generate uuid: %w", err)
		}
		if err := writeFileAtomic(idPath, []byte(newID.String()+"\n"), pubFileMode); err != nil {
			return fmt.Errorf("identity: write %q: %w", idPath, err)
		}
		i.id = newID
	} else {
		return fmt.Errorf("identity: read %q: %w", idPath, err)
	}

	// keypair
	keyPath := filepath.Join(i.dir, keyFile)
	pubPath := filepath.Join(i.dir, pubFile)
	if keyPEM, err := os.ReadFile(keyPath); err == nil {
		block, _ := pem.Decode(keyPEM)
		if block == nil {
			return fmt.Errorf("identity: decode %q: no PEM block", keyPath)
		}
		priv, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return fmt.Errorf("identity: parse %q: %w", keyPath, err)
		}
		i.priv = priv
	} else if errors.Is(err, os.ErrNotExist) {
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return fmt.Errorf("identity: generate keypair: %w", err)
		}
		keyDER, err := x509.MarshalECPrivateKey(priv)
		if err != nil {
			return fmt.Errorf("identity: marshal priv: %w", err)
		}
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
		if err := writeFileAtomic(keyPath, keyPEM, keyFileMode); err != nil {
			return fmt.Errorf("identity: write %q: %w", keyPath, err)
		}
		pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
		if err != nil {
			return fmt.Errorf("identity: marshal pub: %w", err)
		}
		pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
		if err := writeFileAtomic(pubPath, pubPEM, pubFileMode); err != nil {
			return fmt.Errorf("identity: write %q: %w", pubPath, err)
		}
		i.priv = priv
	} else {
		return fmt.Errorf("identity: read %q: %w", keyPath, err)
	}

	// cert + chain (optional — present only after pairing)
	if data, err := os.ReadFile(filepath.Join(i.dir, certFile)); err == nil {
		i.cert = data
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("identity: read cert: %w", err)
	}
	if data, err := os.ReadFile(filepath.Join(i.dir, chainFile)); err == nil {
		i.chain = data
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("identity: read chain: %w", err)
	}
	if data, err := os.ReadFile(filepath.Join(i.dir, pinnedRoots)); err == nil {
		var roots []PinnedRoot
		if err := json.Unmarshal(data, &roots); err != nil {
			return fmt.Errorf("identity: parse pinned roots: %w", err)
		}
		i.roots = roots
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("identity: read pinned roots: %w", err)
	}
	return nil
}

// ID returns the recorder's UUIDv7. Stable for the life of the
// install.
func (i *Identity) ID() uuid.UUID {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.id
}

// PublicKey returns the ECDSA public key (a copy is safe; we return
// a pointer because the underlying ecdsa.PublicKey is immutable
// once generated).
func (i *Identity) PublicKey() *ecdsa.PublicKey {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return &i.priv.PublicKey
}

// PrivateKey returns the ECDSA private key. Used to build CSRs and
// to sign mTLS handshakes. Caller must not mutate.
func (i *Identity) PrivateKey() *ecdsa.PrivateKey {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.priv
}

// IssuedCert returns the PEM-encoded issued mTLS cert, or nil if the
// recorder hasn't been paired yet.
func (i *Identity) IssuedCert() []byte {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if len(i.cert) == 0 {
		return nil
	}
	c := make([]byte, len(i.cert))
	copy(c, i.cert)
	return c
}

// IssuingChain returns the PEM-encoded chain back to the MS root,
// or nil if unpaired.
func (i *Identity) IssuingChain() []byte {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if len(i.chain) == 0 {
		return nil
	}
	c := make([]byte, len(i.chain))
	copy(c, i.chain)
	return c
}

// PinnedRoots returns a copy of the pinned-roots list.
func (i *Identity) PinnedRoots() []PinnedRoot {
	i.mu.RLock()
	defer i.mu.RUnlock()
	out := make([]PinnedRoot, len(i.roots))
	copy(out, i.roots)
	return out
}

// IsPaired returns true iff the recorder has a non-empty issued cert
// + a pinned root + the cert is currently within its validity window.
// (Validity is checked by parsing the cert; expired certs surface as
// IsPaired=false so the recorder won't attempt to use them.)
func (i *Identity) IsPaired() bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if len(i.cert) == 0 || len(i.roots) == 0 {
		return false
	}
	block, _ := pem.Decode(i.cert)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	now := time.Now()
	return now.After(cert.NotBefore) && now.Before(cert.NotAfter)
}

// SetIssuedIdentity persists a freshly-issued cert + chain + pinned
// roots to disk and updates the in-memory state. Called by the
// pairing client at approval time.
func (i *Identity) SetIssuedIdentity(certPEM, chainPEM []byte, pinned []PinnedRoot) error {
	if len(certPEM) == 0 {
		return errors.New("identity: empty cert pem")
	}
	if len(pinned) == 0 {
		return errors.New("identity: at least one pinned root required")
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	if err := writeFileAtomic(filepath.Join(i.dir, certFile), certPEM, pubFileMode); err != nil {
		return err
	}
	if len(chainPEM) > 0 {
		if err := writeFileAtomic(filepath.Join(i.dir, chainFile), chainPEM, pubFileMode); err != nil {
			return err
		}
	}
	rootsJSON, err := json.MarshalIndent(pinned, "", "  ")
	if err != nil {
		return fmt.Errorf("identity: marshal pinned roots: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(i.dir, pinnedRoots), rootsJSON, pubFileMode); err != nil {
		return err
	}

	i.cert = certPEM
	i.chain = chainPEM
	i.roots = pinned
	return nil
}

// ClearIssuedIdentity wipes the issued cert + chain + pinned roots
// from disk and from in-memory state, returning the recorder to its
// unpaired state. The UUIDv7 and ECDSA keypair are kept — per ADR
// 0002 D3 those are stable for the life of the install.
//
// Used by the recorder-local "unpair" path (an operator deciding to
// detach this recorder from its current MS, e.g. to re-pair with a
// different MS or to recover from a mis-pairing). Distinct from the
// MS-initiated unpair flow per pairing-flows.md §2.5, which lands
// when the recorder ↔ MS WebSocket exists. After this call, IsPaired
// returns false, /v1/recorder/identity returns paired=false, and the
// next pairing flow can run cleanly.
//
// Idempotent: calling on an already-unpaired identity is a no-op.
func (i *Identity) ClearIssuedIdentity() error {
	i.mu.Lock()
	defer i.mu.Unlock()

	for _, name := range []string{certFile, chainFile, pinnedRoots} {
		if err := os.Remove(filepath.Join(i.dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("identity: remove %s: %w", name, err)
		}
	}
	i.cert = nil
	i.chain = nil
	i.roots = nil
	return nil
}

// BuildCSR builds an X.509 CertificateRequest signed with the
// recorder's private key, with a SAN URI of
// raikada://recording_server/<id> per ADR 0011 D3.
func (i *Identity) BuildCSR() ([]byte, error) {
	i.mu.RLock()
	priv := i.priv
	id := i.id
	i.mu.RUnlock()

	sanURL, err := url.Parse(fmt.Sprintf("raikada://recording_server/%s", id.String()))
	if err != nil {
		return nil, fmt.Errorf("identity: build SAN: %w", err)
	}
	template := &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   fmt.Sprintf("recording_server/%s", id.String()),
			Organization: []string{"Raikada"},
		},
		URIs: []*url.URL{sanURL},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, template, priv)
	if err != nil {
		return nil, fmt.Errorf("identity: create CSR: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}), nil
}

// PublicKeyFingerprint returns the lower-case hex SHA-256 fingerprint
// of the recorder's public-key PKIX-DER encoding. Useful for
// operator-side fingerprint display in setup wizards.
func (i *Identity) PublicKeyFingerprint() (string, error) {
	i.mu.RLock()
	pub := &i.priv.PublicKey
	i.mu.RUnlock()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:]), nil
}

// writeFileAtomic writes data to path via a temp file + rename so
// readers never observe a partial write.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath) // no-op if rename succeeded
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

// stripTrailing strips trailing whitespace + newlines from a byte
// slice. Used when reading the id file (which we write with a
// trailing newline for cli-friendliness).
func stripTrailing(b []byte) []byte {
	for len(b) > 0 {
		c := b[len(b)-1]
		if c == '\n' || c == '\r' || c == ' ' || c == '\t' {
			b = b[:len(b)-1]
			continue
		}
		break
	}
	return b
}
