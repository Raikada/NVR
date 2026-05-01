package identity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClearIssuedIdentity(t *testing.T) {
	dir := t.TempDir()
	id, err := Open(dir)
	require.NoError(t, err)
	require.False(t, id.IsPaired())

	// Provision an issued cert (same fixture pattern as the round-trip
	// test) so we have something to clear.
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	leafDER, _ := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, id.PublicKey(), caKey)
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	chainPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	require.NoError(t, id.SetIssuedIdentity(leafPEM, chainPEM, []PinnedRoot{{
		FingerprintSHA256: "abc",
		CertPEM:           string(chainPEM),
		Kind:              "ms_self_signed",
		PinnedAt:          time.Now(),
	}}, &MSMetadata{IssuerURL: "https://test.local"}))
	require.True(t, id.IsPaired())

	// Capture the pre-clear UUIDv7 + public key so we can confirm
	// they survive the clear (per ADR 0002 D3 they're stable for the
	// life of the install).
	preID := id.ID()
	prePub := id.PublicKey()

	require.NoError(t, id.ClearIssuedIdentity())
	require.False(t, id.IsPaired())
	require.Empty(t, id.IssuedCert())
	require.Empty(t, id.IssuingChain())
	require.Empty(t, id.PinnedRoots())

	// Cert/chain/pinned-roots files should be gone from disk.
	for _, f := range []string{certFile, chainFile, pinnedRoots} {
		_, err := openStat(filepath.Join(dir, f))
		require.Error(t, err, "expected %s to be removed", f)
	}

	// id and keypair must still be the same.
	require.Equal(t, preID, id.ID())
	require.Equal(t, prePub.X.Bytes(), id.PublicKey().X.Bytes())

	// Re-opening the identity must surface the same id and keypair.
	id2, err := Open(dir)
	require.NoError(t, err)
	require.Equal(t, preID, id2.ID())
	require.False(t, id2.IsPaired())

	// Idempotent: clearing again is fine.
	require.NoError(t, id.ClearIssuedIdentity())
}
