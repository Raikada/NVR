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

func TestOpenGeneratesAndPersists(t *testing.T) {
	dir := t.TempDir()
	id1, err := Open(dir)
	require.NoError(t, err)
	require.NotEqual(t, "00000000-0000-0000-0000-000000000000", id1.ID().String())
	require.Equal(t, byte(0x70&0xf0), byte(id1.ID()[6]&0xf0)) // UUIDv7 version nibble
	require.NotNil(t, id1.PublicKey())
	require.False(t, id1.IsPaired())

	// Re-open: id and keypair should be stable.
	id2, err := Open(dir)
	require.NoError(t, err)
	require.Equal(t, id1.ID(), id2.ID())
	require.Equal(t, id1.PublicKey().X.Bytes(), id2.PublicKey().X.Bytes())
	require.Equal(t, id1.PublicKey().Y.Bytes(), id2.PublicKey().Y.Bytes())
}

func TestBuildCSRHasCorrectSAN(t *testing.T) {
	dir := t.TempDir()
	id, err := Open(dir)
	require.NoError(t, err)
	csrPEM, err := id.BuildCSR()
	require.NoError(t, err)

	block, _ := pem.Decode(csrPEM)
	require.NotNil(t, block)
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	require.NoError(t, err)
	require.NoError(t, csr.CheckSignature())
	require.Len(t, csr.URIs, 1)
	require.Equal(t, "raikada", csr.URIs[0].Scheme)
	require.Equal(t, "recording_server", csr.URIs[0].Host)
	require.Equal(t, "/"+id.ID().String(), csr.URIs[0].Path)
}

func TestSetIssuedIdentity(t *testing.T) {
	dir := t.TempDir()
	id, err := Open(dir)
	require.NoError(t, err)
	require.False(t, id.IsPaired())

	// Sign a cert against the recorder's pubkey using a fake CA so
	// the validity window is realistic.
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

	root := PinnedRoot{
		FingerprintSHA256: "abc",
		CertPEM:           string(chainPEM),
		NotBefore:         caTemplate.NotBefore,
		NotAfter:          caTemplate.NotAfter,
		Kind:              "ms_self_signed",
		PinnedAt:          time.Now(),
	}
	require.NoError(t, id.SetIssuedIdentity(leafPEM, chainPEM, []PinnedRoot{root}, nil))
	require.True(t, id.IsPaired())
	require.NotEmpty(t, id.IssuedCert())
	require.NotEmpty(t, id.IssuingChain())
	require.Len(t, id.PinnedRoots(), 1)

	// Reopen and confirm persistence.
	id2, err := Open(dir)
	require.NoError(t, err)
	require.True(t, id2.IsPaired())
	require.Equal(t, id.ID(), id2.ID())
	require.Equal(t, "abc", id2.PinnedRoots()[0].FingerprintSHA256)
}

func TestKeyFileIsMode0600(t *testing.T) {
	dir := t.TempDir()
	_, err := Open(dir)
	require.NoError(t, err)
	info, err := openStat(filepath.Join(dir, keyFile))
	require.NoError(t, err)
	require.Equal(t, keyFileMode, int(info.Mode().Perm()))
}

func TestExpiredCertNotPaired(t *testing.T) {
	dir := t.TempDir()
	id, err := Open(dir)
	require.NoError(t, err)

	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-2 * time.Hour),
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
		NotBefore:    time.Now().Add(-2 * time.Hour),
		NotAfter:     time.Now().Add(-time.Hour), // expired
	}
	leafDER, _ := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, id.PublicKey(), caKey)
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	chainPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	root := PinnedRoot{FingerprintSHA256: "x", CertPEM: string(chainPEM)}
	require.NoError(t, id.SetIssuedIdentity(leafPEM, chainPEM, []PinnedRoot{root}, nil))
	require.False(t, id.IsPaired(), "expired cert should not count as paired")
}
