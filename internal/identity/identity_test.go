package identity

import (
	"crypto/x509"
	"encoding/pem"
	"path/filepath"
	"testing"

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
