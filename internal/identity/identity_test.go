package identity

import (
	"crypto/x509"
	"encoding/pem"
	"os"
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

func TestKeyFileIsMode0600(t *testing.T) {
	dir := t.TempDir()
	_, err := Open(dir)
	require.NoError(t, err)
	info, err := openStat(filepath.Join(dir, keyFile))
	require.NoError(t, err)
	require.Equal(t, keyFileMode, int(info.Mode().Perm()))
}

func TestPublicKeyFingerprintStable(t *testing.T) {
	dir := t.TempDir()
	id, err := Open(dir)
	require.NoError(t, err)
	fp1, err := id.PublicKeyFingerprint()
	require.NoError(t, err)
	require.NotEmpty(t, fp1)

	// Reopening yields the same fingerprint.
	id2, err := Open(dir)
	require.NoError(t, err)
	fp2, err := id2.PublicKeyFingerprint()
	require.NoError(t, err)
	require.Equal(t, fp1, fp2)
}

// TestTLSPairGeneratedOnFirstStart verifies Open creates the tls.crt /
// tls.key pair and the cert parses cleanly with the expected SANs.
func TestTLSPairGeneratedOnFirstStart(t *testing.T) {
	dir := t.TempDir()
	id, err := Open(dir)
	require.NoError(t, err)

	certPath, keyPath := id.TLSPaths()
	require.FileExists(t, certPath)
	require.FileExists(t, keyPath)

	// Key must be mode 0600.
	keyInfo, err := os.Stat(keyPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(keyFileMode), keyInfo.Mode().Perm())

	pemBytes, err := os.ReadFile(certPath)
	require.NoError(t, err)
	block, _ := pem.Decode(pemBytes)
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	// SANs include localhost + <id>.local + at least one IP.
	require.Contains(t, cert.DNSNames, "localhost")
	require.Contains(t, cert.DNSNames, id.ID().String()+".local")
	require.NotEmpty(t, cert.IPAddresses)

	// Validity is 1 year (allow 1 minute slack for test runtime).
	require.WithinDuration(t, time.Now().Add(tlsValidity), cert.NotAfter, time.Hour)
}

// TestTLSPairStableOnReopen verifies a healthy cert is preserved across
// a re-Open() call.
func TestTLSPairStableOnReopen(t *testing.T) {
	dir := t.TempDir()
	id1, err := Open(dir)
	require.NoError(t, err)
	certPath, _ := id1.TLSPaths()
	first, err := os.ReadFile(certPath)
	require.NoError(t, err)

	// Re-open: certificate bytes should be byte-for-byte identical.
	_, err = Open(dir)
	require.NoError(t, err)
	second, err := os.ReadFile(certPath)
	require.NoError(t, err)
	require.Equal(t, first, second)
}

// TestTLSPairRegenerateOnExpiringSoon verifies the renewal threshold:
// a cert with NotAfter < tlsRenewWithin away triggers regeneration on
// the next Open.
func TestTLSPairRegenerateOnExpiringSoon(t *testing.T) {
	dir := t.TempDir()
	_, err := Open(dir)
	require.NoError(t, err)
	certPath := filepath.Join(dir, tlsCertFile)
	keyPath := filepath.Join(dir, tlsKeyFile)

	// Replace cert with one whose NotAfter is 1 day in the future,
	// well inside the 30-day renew window.
	expiringCert := generateExpiringTLSPair(t, time.Now().Add(24*time.Hour))
	require.NoError(t, os.WriteFile(certPath, expiringCert.certPEM, pubFileMode))
	require.NoError(t, os.WriteFile(keyPath, expiringCert.keyPEM, keyFileMode))

	beforeBytes, err := os.ReadFile(certPath)
	require.NoError(t, err)

	// Re-open: should regenerate.
	_, err = Open(dir)
	require.NoError(t, err)
	afterBytes, err := os.ReadFile(certPath)
	require.NoError(t, err)
	require.NotEqual(t, beforeBytes, afterBytes)
}
