// Package identity manages the recorder's persistent device identity:
// the self-generated UUIDv7 (per ADR 0002 D3) and the ECDSA P-256
// keypair the recorder uses to sign locally-issued JWTs and similar
// per-recorder signed material.
//
// Storage lives in a single directory whose path is operator-
// configured (defaults to "<confDir>/identity"). The recorder
// generates the id and keypair on first start so an unpaired
// recorder still has a stable identity.
//
// On-disk layout:
//
//	<dir>/
//	  id             — UUIDv7 string (text)
//	  recorder.key   — ECDSA P-256 private key (PEM, mode 0600)
//	  recorder.pub   — public key (PEM, mode 0644)
//	  tls.crt        — self-signed HTTPS certificate (PEM, mode 0644)
//	  tls.key        — TLS private key (PEM, mode 0600)
//
// Filesystem-permissions-as-protection is the v1 approach.
//
// The TLS pair is generated on first start so the API listener has a
// usable HTTPS surface without operator intervention. SANs cover the
// hostname, "<id>.local" (matches the mDNS instance), "localhost", and
// every IPv4 address bound to the host's NICs. The certificate is
// renewed on Open() when the existing cert is within 30d of expiry.
package identity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	idFile      = "id"
	keyFile     = "recorder.key"
	pubFile     = "recorder.pub"
	tlsCertFile = "tls.crt"
	tlsKeyFile  = "tls.key"
	keyFileMode = 0o600
	pubFileMode = 0o644
	dirMode     = 0o700

	// tlsValidity is the lifetime of self-generated TLS certificates.
	tlsValidity = 365 * 24 * time.Hour
	// tlsRenewWithin triggers a regeneration when the existing certificate
	// is within this window of expiry. 30 days is the same threshold the
	// API's TLS-replace UI surfaces, so manual replacement and auto-renew
	// agree.
	tlsRenewWithin = 30 * 24 * time.Hour
)

// Identity is the recorder's persistent identity. Read-mostly: the id
// and keypair are set once at first install and never change for the
// life of the recorder (per ADR 0002 D3).
type Identity struct {
	dir string
	mu  sync.RWMutex

	id   uuid.UUID
	priv *ecdsa.PrivateKey
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
	if err := id.ensureTLSPair(); err != nil {
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

	return nil
}

// ensureTLSPair makes sure tls.crt + tls.key exist under the identity
// directory and are not within tlsRenewWithin of expiry. Generates
// fresh material when missing or expiring soon. Idempotent.
func (i *Identity) ensureTLSPair() error {
	certPath := filepath.Join(i.dir, tlsCertFile)
	keyPath := filepath.Join(i.dir, tlsKeyFile)

	regen := false
	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	switch {
	case errors.Is(certErr, os.ErrNotExist) || errors.Is(keyErr, os.ErrNotExist):
		regen = true
	case certErr != nil:
		return fmt.Errorf("identity: read %q: %w", certPath, certErr)
	case keyErr != nil:
		return fmt.Errorf("identity: read %q: %w", keyPath, keyErr)
	default:
		// Parse cert; if expiring soon or unparseable, regenerate.
		block, _ := pem.Decode(certPEM)
		if block == nil {
			regen = true
			break
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			regen = true
			break
		}
		if time.Until(cert.NotAfter) < tlsRenewWithin {
			regen = true
		}
		_ = keyPEM // present and valid pair; no further action
	}

	if !regen {
		return nil
	}
	return i.generateTLSPair(certPath, keyPath)
}

// generateTLSPair writes a fresh self-signed certificate + key pair to
// the supplied paths. SANs include the hostname, "<id>.local", "localhost",
// and every IPv4 address found on the host's NICs.
func (i *Identity) generateTLSPair(certPath, keyPath string) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("identity: generate tls key: %w", err)
	}

	dnsNames, ipAddrs := i.tlsSANs()

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("identity: tls serial: %w", err)
	}

	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "raikada-nvr-" + i.id.String(),
			Organization: []string{"Raikada"},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(tlsValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		DNSNames:              dnsNames,
		IPAddresses:           ipAddrs,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("identity: create tls cert: %w", err)
	}
	certPEMBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := writeFileAtomic(certPath, certPEMBytes, pubFileMode); err != nil {
		return fmt.Errorf("identity: write %q: %w", certPath, err)
	}

	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return fmt.Errorf("identity: marshal tls key: %w", err)
	}
	keyPEMBytes := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := writeFileAtomic(keyPath, keyPEMBytes, keyFileMode); err != nil {
		return fmt.Errorf("identity: write %q: %w", keyPath, err)
	}
	return nil
}

// tlsSANs returns the DNS names and IP addresses to embed in the
// self-signed certificate. The set is deduplicated.
func (i *Identity) tlsSANs() ([]string, []net.IP) {
	seenDNS := map[string]struct{}{}
	addDNS := func(s string) {
		if s == "" {
			return
		}
		if _, ok := seenDNS[s]; ok {
			return
		}
		seenDNS[s] = struct{}{}
	}

	addDNS("localhost")
	addDNS(i.id.String() + ".local")
	if host, err := os.Hostname(); err == nil && host != "" {
		addDNS(host)
		// Common form on macOS / Linux LANs.
		addDNS(host + ".local")
	}

	dns := make([]string, 0, len(seenDNS))
	for k := range seenDNS {
		dns = append(dns, k)
	}

	seenIP := map[string]struct{}{}
	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	for _, ip := range ips {
		seenIP[ip.String()] = struct{}{}
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP
			if ip == nil || ip.IsUnspecified() {
				continue
			}
			// Restrict to IPv4 NICs as the plan asks; loopback/IPv4-link-local OK.
			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}
			if _, dup := seenIP[ip4.String()]; dup {
				continue
			}
			seenIP[ip4.String()] = struct{}{}
			ips = append(ips, ip4)
		}
	}
	return dns, ips
}

// ID returns the recorder's UUIDv7. Stable for the life of the
// install.
func (i *Identity) ID() uuid.UUID {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.id
}

// Dir returns the on-disk directory housing the identity material.
// Phase 5 callers (system TLS PUT, bootstrap admin password file, etc.)
// use this to derive sibling file paths like tls.crt / tls.key without
// re-deriving the path.
func (i *Identity) Dir() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.dir
}

// PublicKey returns the ECDSA public key (a copy is safe; we return
// a pointer because the underlying ecdsa.PublicKey is immutable
// once generated).
func (i *Identity) PublicKey() *ecdsa.PublicKey {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return &i.priv.PublicKey
}

// PrivateKey returns the ECDSA private key. Used to sign locally-
// issued JWTs and similar per-recorder signed material. Caller must
// not mutate.
func (i *Identity) PrivateKey() *ecdsa.PrivateKey {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.priv
}

// TLSPaths returns the absolute paths of the self-generated TLS
// certificate and key. Callers (Core, the API constructor, the
// fsnotify reload watcher) use these to point HTTPS listeners at the
// identity-managed pair.
func (i *Identity) TLSPaths() (certPath, keyPath string) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return filepath.Join(i.dir, tlsCertFile), filepath.Join(i.dir, tlsKeyFile)
}

// BuildCSR builds an X.509 CertificateRequest signed with the
// recorder's private key, with a SAN URI of
// raikada://recording_server/<id>. Useful for any future enrollment /
// identity-attestation flow that wants a recorder-signed CSR.
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
