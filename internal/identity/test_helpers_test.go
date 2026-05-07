package identity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

// expiringTLSPair holds the PEM-encoded cert + key emitted by
// generateExpiringTLSPair. Used by TestTLSPairRegenerateOnExpiringSoon
// to seed an "almost expired" pair on disk.
type expiringTLSPair struct {
	certPEM []byte
	keyPEM  []byte
}

// generateExpiringTLSPair returns a self-signed cert with NotAfter ==
// expiresAt. Test-only helper.
func generateExpiringTLSPair(t *testing.T, expiresAt time.Time) expiringTLSPair {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "expiring-test",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              expiresAt,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return expiringTLSPair{
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		keyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}
}
