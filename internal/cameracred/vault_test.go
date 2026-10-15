package cameracred

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVault_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	v, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	plain := []byte("hunter2")
	ct, nonce, err := v.Encrypt(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	got, err := v.Decrypt(ct, nonce)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(got) != string(plain) {
		t.Errorf("round trip: got %q want %q", got, plain)
	}
}

func TestVault_OpenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	v1, err := Open(dir)
	if err != nil {
		t.Fatalf("open1: %v", err)
	}
	ct, nonce, _ := v1.Encrypt([]byte("secret"))

	// Re-open with the same dir; should use the same key file.
	v2, err := Open(dir)
	if err != nil {
		t.Fatalf("open2: %v", err)
	}
	got, err := v2.Decrypt(ct, nonce)
	if err != nil {
		t.Fatalf("decrypt with re-opened vault: %v", err)
	}
	if string(got) != "secret" {
		t.Errorf("got %q", got)
	}
}

func TestVault_DecryptFailsOnTamper(t *testing.T) {
	dir := t.TempDir()
	v, _ := Open(dir)
	ct, nonce, _ := v.Encrypt([]byte("secret"))
	ct[0] ^= 0xff // flip a bit
	if _, err := v.Decrypt(ct, nonce); err == nil {
		t.Fatal("expected decrypt error on tampered ciphertext, got nil")
	}
}

func TestVault_KeyFilePermissions(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatalf("open: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "cred.key"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected 0600, got %o", perm)
	}
}
