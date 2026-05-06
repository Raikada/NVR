package softwareupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
)

// genKey is the test-only keypair generator.
func genKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	return pub, priv
}

// signManifest produces a SignedManifest pair.
func signManifest(t *testing.T, priv ed25519.PrivateKey, m Manifest) string {
	t.Helper()
	canonical, err := Canonical(m)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	sig := ed25519.Sign(priv, canonical)
	return base64.StdEncoding.EncodeToString(sig)
}

// canned manifest for the local hardware.
func freshManifest(t *testing.T, sha256 string, size int64) Manifest {
	t.Helper()
	return Manifest{
		ID:                uuid.NewString(),
		ApplianceKind:     "recording_server",
		Version:           "1.2.3",
		Channel:           "stable",
		ArtifactSHA256:    sha256,
		ArtifactSizeBytes: size,
		Compatibility:     Compat{Hardware: []string{runtime.GOOS + "/" + runtime.GOARCH}},
		ReleasedAt:        "2026-05-06T00:00:00.000Z",
	}
}

func TestApplier_PreflightSignatureFails(t *testing.T) {
	pub, priv := genKey(t)
	_ = priv
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "recorder")
	if err := os.WriteFile(binPath, []byte("OLD"), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	stateStore, err := NewStateStore(tmpDir)
	if err != nil {
		t.Fatalf("state store: %v", err)
	}
	ap := &Applier{
		BinaryPath:     binPath,
		PublicKey:      pub,
		State:          stateStore,
		CurrentVersion: "1.0.0",
	}
	m := freshManifest(t, "deadbeef", 3)
	// Empty / wrong signature.
	err = ap.Apply(context.Background(), ApplyOptions{
		Manifest:      m,
		Signature:     "",
		ArtifactBytes: []byte("NEW"),
	})
	if err == nil {
		t.Fatal("expected signature failure, got nil")
	}
	// Binary was not modified.
	got, _ := os.ReadFile(binPath)
	if string(got) != "OLD" {
		t.Fatalf("binary was modified despite preflight failure: %q", string(got))
	}
	// No backup file was created.
	if _, err := os.Stat(binPath + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(".bak should not exist after preflight signature failure")
	}
}

func TestApplier_PreflightSHA256Fails(t *testing.T) {
	pub, priv := genKey(t)
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "recorder")
	if err := os.WriteFile(binPath, []byte("OLD"), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	stateStore, _ := NewStateStore(tmpDir)
	ap := &Applier{
		BinaryPath:     binPath,
		PublicKey:      pub,
		State:          stateStore,
		CurrentVersion: "1.0.0",
	}
	// Sign a manifest claiming a specific SHA. Then provide bytes that
	// don't match.
	artifact := []byte("NEW")
	wrongSha := "0000000000000000000000000000000000000000000000000000000000000000"
	m := freshManifest(t, wrongSha, int64(len(artifact)))
	sig := signManifest(t, priv, m)
	err := ap.Apply(context.Background(), ApplyOptions{
		Manifest:      m,
		Signature:     sig,
		ArtifactBytes: artifact,
	})
	if err == nil {
		t.Fatal("expected sha256 mismatch, got nil")
	}
	got, _ := os.ReadFile(binPath)
	if string(got) != "OLD" {
		t.Fatalf("binary was modified despite SHA256 mismatch: %q", string(got))
	}
}

func TestApplier_PreflightCompatFails(t *testing.T) {
	pub, priv := genKey(t)
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "recorder")
	if err := os.WriteFile(binPath, []byte("OLD"), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	stateStore, _ := NewStateStore(tmpDir)
	ap := &Applier{
		BinaryPath:     binPath,
		PublicKey:      pub,
		State:          stateStore,
		CurrentVersion: "1.0.0",
	}
	artifact := []byte("NEW")
	sha := mustSHA(artifact)
	m := freshManifest(t, sha, int64(len(artifact)))
	// Override compat to force a mismatch.
	m.Compatibility = Compat{Hardware: []string{"freebsd/sparc"}}
	sig := signManifest(t, priv, m)
	err := ap.Apply(context.Background(), ApplyOptions{
		Manifest:         m,
		Signature:        sig,
		ArtifactBytes:    artifact,
		RecorderHardware: runtime.GOOS + "/" + runtime.GOARCH,
	})
	if !errors.Is(err, ErrIncompatible) {
		t.Fatalf("expected ErrIncompatible, got %v", err)
	}
}

func TestApplier_BackupAndSwap_Success(t *testing.T) {
	pub, priv := genKey(t)
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "recorder")
	if err := os.WriteFile(binPath, []byte("OLD"), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	stateStore, _ := NewStateStore(tmpDir)
	ap := &Applier{
		BinaryPath:     binPath,
		PublicKey:      pub,
		State:          stateStore,
		CurrentVersion: "1.0.0",
	}
	artifact := []byte("NEW BYTES")
	sha := mustSHA(artifact)
	m := freshManifest(t, sha, int64(len(artifact)))
	sig := signManifest(t, priv, m)

	if err := ap.Apply(context.Background(), ApplyOptions{
		Manifest:      m,
		Signature:     sig,
		ArtifactBytes: artifact,
		LifecycleID:   "lc-1",
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// New binary in place.
	got, _ := os.ReadFile(binPath)
	if string(got) != "NEW BYTES" {
		t.Fatalf("binary not replaced: %q", string(got))
	}
	// Backup of old binary present.
	bak, err := os.ReadFile(binPath + ".bak")
	if err != nil {
		t.Fatalf("backup file missing: %v", err)
	}
	if string(bak) != "OLD" {
		t.Fatalf("backup file contains wrong bytes: %q", string(bak))
	}
	// State updated.
	st, _ := stateStore.Load()
	if st.LastAppliedLifecycleID != "lc-1" {
		t.Fatalf("LastAppliedLifecycleID = %q, want lc-1", st.LastAppliedLifecycleID)
	}
	if st.PreviousVersion != "1.0.0" {
		t.Fatalf("PreviousVersion = %q, want 1.0.0", st.PreviousVersion)
	}
	if st.CurrentVersion != "1.2.3" {
		t.Fatalf("CurrentVersion = %q, want 1.2.3", st.CurrentVersion)
	}
}

func TestApplier_PostRestart_HealthCheckFailRestoresBackup(t *testing.T) {
	pub, _ := genKey(t)
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "recorder")
	// Simulate the post-swap state: binary contains NEW; .bak contains OLD.
	if err := os.WriteFile(binPath, []byte("NEW"), 0o755); err != nil {
		t.Fatalf("write new: %v", err)
	}
	if err := os.WriteFile(binPath+".bak", []byte("OLD"), 0o755); err != nil {
		t.Fatalf("write bak: %v", err)
	}
	stateStore, _ := NewStateStore(tmpDir)
	ap := &Applier{
		BinaryPath: binPath,
		PublicKey:  pub,
		State:      stateStore,
		HealthCheck: func(ctx context.Context) error {
			return errors.New("simulated health-check failure")
		},
	}
	err := ap.PostRestartCheck(context.Background())
	if err == nil {
		t.Fatal("expected health-check error, got nil")
	}
	// Binary restored to OLD.
	got, _ := os.ReadFile(binPath)
	if string(got) != "OLD" {
		t.Fatalf("binary not restored: %q", string(got))
	}
}

func TestApplier_PostRestart_HealthCheckPass(t *testing.T) {
	pub, _ := genKey(t)
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "recorder")
	if err := os.WriteFile(binPath, []byte("NEW"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	stateStore, _ := NewStateStore(tmpDir)
	ap := &Applier{
		BinaryPath: binPath,
		PublicKey:  pub,
		State:      stateStore,
		HealthCheck: func(ctx context.Context) error {
			return nil
		},
	}
	if err := ap.PostRestartCheck(context.Background()); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	// Binary stays as NEW.
	got, _ := os.ReadFile(binPath)
	if string(got) != "NEW" {
		t.Fatalf("binary unexpectedly modified: %q", string(got))
	}
}

func TestApplier_PostRestart_NoHealthCheck_PassThrough(t *testing.T) {
	pub, _ := genKey(t)
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "recorder")
	if err := os.WriteFile(binPath, []byte("NEW"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	stateStore, _ := NewStateStore(tmpDir)
	ap := &Applier{BinaryPath: binPath, PublicKey: pub, State: stateStore}
	if err := ap.PostRestartCheck(context.Background()); err != nil {
		t.Fatalf("expected pass-through nil, got %v", err)
	}
}

func TestStateStore_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	s, err := NewStateStore(tmpDir)
	if err != nil {
		t.Fatalf("NewStateStore: %v", err)
	}
	st, _ := s.Load()
	if st.CurrentVersion != "" {
		t.Fatalf("expected empty initial state, got %+v", st)
	}
	st.CurrentVersion = "1.0.0"
	st.LastAppliedAt = time.Now().UTC()
	st.LastAppliedLifecycleID = "abc"
	if err := s.Save(st); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, _ := s.Load()
	if loaded.CurrentVersion != "1.0.0" {
		t.Fatalf("CurrentVersion mismatch: %q", loaded.CurrentVersion)
	}
	if loaded.LastAppliedLifecycleID != "abc" {
		t.Fatalf("LastAppliedLifecycleID mismatch: %q", loaded.LastAppliedLifecycleID)
	}
}

func TestVerify_RoundTrip(t *testing.T) {
	pub, priv := genKey(t)
	m := freshManifest(t, "deadbeef", 1)
	sig := signManifest(t, priv, m)
	if err := VerifyManifest(m, sig, pub); err != nil {
		t.Fatalf("VerifyManifest: %v", err)
	}
	// Tamper.
	bad := m
	bad.Version = "9.9.9"
	if err := VerifyManifest(bad, sig, pub); err == nil {
		t.Fatal("expected tamper to fail")
	}
}

func mustSHA(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
