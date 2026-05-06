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

	"github.com/stretchr/testify/require"
)

// TestRefreshMSMetadataReplacesURLsButKeepsTrust covers the URL-drift
// recovery path: a paired recorder receives a fresh URL via mDNS that
// matches the pinned root fingerprint, and we expect RefreshMSMetadata
// to update only the URL fields without touching the issued cert /
// chain / pinned roots.
func TestRefreshMSMetadataReplacesURLsButKeepsTrust(t *testing.T) {
	dir := t.TempDir()
	id, err := Open(dir)
	require.NoError(t, err)

	// Pair with an MS at the original URL.
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, id.PublicKey(), caKey)
	require.NoError(t, err)
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	chainPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	pinned := []PinnedRoot{{
		FingerprintSHA256: "abc",
		CertPEM:           string(chainPEM),
		NotBefore:         caTemplate.NotBefore,
		NotAfter:          caTemplate.NotAfter,
		Kind:              "ms_self_signed",
		PinnedAt:          time.Now(),
	}}
	original := MSMetadata{
		IssuerURL:    "https://old-ms.local:8443",
		JWKSURL:      "https://old-ms.local:8443/.well-known/jwks.json",
		RootsURL:     "https://old-ms.local:8443/.well-known/raikada-roots",
		WebSocketURL: "wss://old-ms.local:8443/v1/ws",
	}
	require.NoError(t, id.SetIssuedIdentity(leafPEM, chainPEM, pinned, &original))

	// Apply the URL refresh.
	updated := MSMetadata{
		IssuerURL:    "https://new-ms.local:8443",
		JWKSURL:      "https://new-ms.local:8443/.well-known/jwks.json",
		RootsURL:     "https://new-ms.local:8443/.well-known/raikada-roots",
		WebSocketURL: "wss://new-ms.local:8443/v1/ws",
	}
	require.NoError(t, id.RefreshMSMetadata(updated))

	got := id.MSMetadata()
	require.NotNil(t, got)
	require.Equal(t, updated, *got)

	// Pinned roots + cert + chain unchanged.
	require.True(t, id.IsPaired())
	require.NotEmpty(t, id.IssuedCert())
	require.Len(t, id.PinnedRoots(), 1)
	require.Equal(t, "abc", id.PinnedRoots()[0].FingerprintSHA256)

	// Reopen and confirm the refresh persisted.
	id2, err := Open(dir)
	require.NoError(t, err)
	require.Equal(t, "https://new-ms.local:8443", id2.MSMetadata().IssuerURL)
}

// TestRefreshMSMetadataNoOpOnIdenticalInput confirms idempotency.
func TestRefreshMSMetadataNoOpOnIdenticalInput(t *testing.T) {
	dir := t.TempDir()
	id, err := Open(dir)
	require.NoError(t, err)

	// Build minimum valid pairing state.
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)
	leafDER, _ := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(2),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}, caCert, id.PublicKey(), caKey)
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	chainPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	meta := MSMetadata{
		IssuerURL: "https://ms.local:8443",
		JWKSURL:   "https://ms.local:8443/.well-known/jwks.json",
	}
	require.NoError(t, id.SetIssuedIdentity(leafPEM, chainPEM,
		[]PinnedRoot{{FingerprintSHA256: "abc", CertPEM: string(chainPEM)}}, &meta))

	// Refresh with the same value — should be a no-op (no error).
	require.NoError(t, id.RefreshMSMetadata(meta))
	require.Equal(t, meta, *id.MSMetadata())
}

// TestRefreshMSMetadataErrorsWhenUnpaired ensures the writer refuses
// to operate on a recorder that has no pinned trust anchor.
func TestRefreshMSMetadataErrorsWhenUnpaired(t *testing.T) {
	dir := t.TempDir()
	id, err := Open(dir)
	require.NoError(t, err)
	require.False(t, id.IsPaired())

	err = id.RefreshMSMetadata(MSMetadata{IssuerURL: "https://ms.local"})
	require.Error(t, err)
}
