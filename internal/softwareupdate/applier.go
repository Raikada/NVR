package softwareupdate

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/bluenviron/mediamtx/internal/logger"
)

// AuditEmitter is the package-internal seam onto the recorder's
// audit chain. Implemented by the API package; lets the applier
// emit chained software_update.* entries without depending on the
// API's concrete chain type. Mirrors camerasync.AuditEmitter and
// policysync.AuditEmitter.
type AuditEmitter interface {
	Emit(action, outcome string, attrs map[string]string)
}

// Applier owns the install + restart + health-check + rollback
// machinery. One per recorder.
type Applier struct {
	// BinaryPath is the path to the running recorder binary. The
	// applier swaps a new binary into this path under a backup of
	// the existing one. In v1, this is os.Args[0] when an absolute
	// path; tests inject a fixture.
	BinaryPath string

	// PublicKey is the Raikada release ed25519 public key used to
	// re-verify a manifest at apply time.
	PublicKey ed25519.PublicKey

	// State is the on-disk lifecycle tracker.
	State *StateStore

	// Logger
	Logger logger.Writer

	// Audit is the recorder's audit chain (optional in tests).
	Audit AuditEmitter

	// CurrentVersion is the version of the running binary. Stamped
	// onto state.CurrentVersion on first successful apply and used
	// by health checks comparing post-restart state.
	CurrentVersion string

	// SignalSelf is called to trigger a restart after a successful
	// swap. Nil means "do not signal" (tests). Production wiring
	// supplies a function that sends SIGTERM to the process group
	// so the supervisor relaunches.
	SignalSelf func() error

	// HealthCheck is the post-restart probe used after a v1
	// "external supervisor restarts us" path; the recorder writes
	// the pending state and exits, then the new binary on startup
	// reads the state, runs the check, and reports back. In v1 the
	// supervised-restart path is documented as a follow-up — most
	// of the apply happens before the restart, and the health check
	// runs immediately on the next startup.
	HealthCheck func(ctx context.Context) error

	// HTTPClient lets the applier fetch artifact bytes from the
	// manifest's artifact_url when the MS push uses URL-mode rather
	// than inlining bytes.
	HTTPClient *http.Client
}

// ApplyOptions bundles the inputs to Apply. Caller (the API
// handler) collects these from the inbound MS push.
type ApplyOptions struct {
	Manifest         Manifest
	Signature        string
	ArtifactBytes    []byte // inline artifact (if push embedded base64)
	ArtifactURL      string // fallback URL fetch if ArtifactBytes is empty
	LifecycleID      string // MS lifecycle row id (carried for report-back)
	MSVersion        string // currently-paired MS version, for compat check
	RecorderHardware string // typically GOOS/GOARCH
}

// Apply orchestrates the full install path:
//
//   1. Verify signature against the pinned Raikada release public key.
//   2. Verify the artifact bytes match the manifest's SHA-256.
//   3. Verify compatibility against the recorder's MS version + hw.
//   4. Persist a backup of the current binary at <binary>.bak.
//   5. Atomically swap the new binary into BinaryPath.
//   6. Persist state (PendingApprovedLifecycleID → cleared;
//      LastAppliedLifecycleID set; PreviousVersion set; CurrentVersion
//      tentatively set to the new version).
//   7. Emit software_update.preflight_passed audit + start.
//   8. Signal self for restart (optional).
//
// On any failure before step 4 (preflight failures), the applier
// emits software_update.preflight_failed and returns the error
// without mutating disk.
//
// Post-restart health-check + rollback is the responsibility of the
// startup integration: the new binary reads state on startup, runs
// HealthCheck, and either MarksApplied or RestoresAndRollsBack via
// the report-back endpoint.
func (a *Applier) Apply(ctx context.Context, opts ApplyOptions) error {
	if a == nil {
		return errors.New("software_update: nil applier")
	}
	if a.BinaryPath == "" {
		return errors.New("software_update: applier missing BinaryPath")
	}
	if a.PublicKey == nil {
		return errors.New("software_update: applier missing PublicKey")
	}
	if a.State == nil {
		return errors.New("software_update: applier missing StateStore")
	}

	// 1. Signature.
	if err := VerifyManifest(opts.Manifest, opts.Signature, a.PublicKey); err != nil {
		a.emit("software_update.preflight_failed", "failure", map[string]string{
			"reason":      "signature_invalid",
			"version":     opts.Manifest.Version,
			"lifecycle":   opts.LifecycleID,
		})
		return err
	}

	// 2. Artifact bytes — fetch if not inline.
	artifact := opts.ArtifactBytes
	if len(artifact) == 0 {
		if opts.ArtifactURL == "" {
			return errors.New("software_update: no artifact bytes or url")
		}
		fetched, err := a.fetch(ctx, opts.ArtifactURL)
		if err != nil {
			a.emit("software_update.preflight_failed", "failure", map[string]string{
				"reason":    "artifact_fetch_failed",
				"version":   opts.Manifest.Version,
				"lifecycle": opts.LifecycleID,
				"error":     err.Error(),
			})
			return fmt.Errorf("fetch artifact: %w", err)
		}
		artifact = fetched
	}
	if err := VerifyArtifactSHA256(opts.Manifest, artifact); err != nil {
		a.emit("software_update.preflight_failed", "failure", map[string]string{
			"reason":    "artifact_sha256_mismatch",
			"version":   opts.Manifest.Version,
			"lifecycle": opts.LifecycleID,
		})
		return err
	}

	// 3. Compatibility.
	hw := opts.RecorderHardware
	if hw == "" {
		hw = runtime.GOOS + "/" + runtime.GOARCH
	}
	if err := CheckCompatibility(opts.Manifest, opts.MSVersion, hw); err != nil {
		a.emit("software_update.preflight_failed", "failure", map[string]string{
			"reason":    "incompatible",
			"version":   opts.Manifest.Version,
			"lifecycle": opts.LifecycleID,
			"error":     err.Error(),
		})
		return err
	}

	a.emit("software_update.preflight_passed", "success", map[string]string{
		"version":   opts.Manifest.Version,
		"lifecycle": opts.LifecycleID,
	})

	// 4 + 5. Backup + swap.
	if err := backupAndSwap(a.BinaryPath, artifact); err != nil {
		a.emit("software_update.failed", "failure", map[string]string{
			"reason":    "swap_failed",
			"version":   opts.Manifest.Version,
			"lifecycle": opts.LifecycleID,
			"error":     err.Error(),
		})
		return err
	}

	// 6. Persist state.
	st, err := a.State.Load()
	if err != nil {
		a.warn("software_update: load state pre-save: %v", err)
		st = &State{}
	}
	st.PendingApprovedLifecycleID = ""
	st.PendingTargetVersion = ""
	st.LastAppliedLifecycleID = opts.LifecycleID
	st.LastAppliedAt = time.Now().UTC()
	st.PreviousVersion = a.CurrentVersion
	st.CurrentVersion = opts.Manifest.Version
	st.LastFailureReason = ""
	if err := a.State.Save(st); err != nil {
		a.warn("software_update: save state: %v", err)
	}

	// 7. Started audit.
	a.emit("software_update.started", "success", map[string]string{
		"version":         opts.Manifest.Version,
		"previous":        a.CurrentVersion,
		"lifecycle":       opts.LifecycleID,
	})

	// 8. Signal self for restart (production wiring).
	if a.SignalSelf != nil {
		if err := a.SignalSelf(); err != nil {
			a.warn("software_update: signal self: %v", err)
		}
	}
	return nil
}

// PostRestartCheck runs the recorder-side post-restart health check.
// Called from the recorder's startup path after a binary swap. If
// the health check passes, returns no error. If it fails, restores
// the previous binary by swapping the .bak file back into place and
// returns the error so the caller can audit + report-failed.
//
// The caller is responsible for the report-back to the MS via the
// existing recorder ↔ MS link.
func (a *Applier) PostRestartCheck(ctx context.Context) error {
	if a.HealthCheck == nil {
		return nil // no check configured; treat as pass
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	err := a.HealthCheck(timeoutCtx)
	if err == nil {
		return nil
	}
	// Health check failed — restore.
	st, _ := a.State.Load()
	a.emit("software_update.failed", "failure", map[string]string{
		"reason":    "health_check_failed",
		"lifecycle": stateLifecycle(st),
		"error":     err.Error(),
	})
	a.emit("software_update.rollback_started", "success", map[string]string{
		"lifecycle": stateLifecycle(st),
	})
	if rerr := restoreBackup(a.BinaryPath); rerr != nil {
		a.emit("software_update.rollback_failed", "failure", map[string]string{
			"lifecycle": stateLifecycle(st),
			"error":     rerr.Error(),
		})
		return fmt.Errorf("rollback restore: %v (after health-check err: %w)", rerr, err)
	}
	a.emit("software_update.rollback_succeeded", "success", map[string]string{
		"lifecycle": stateLifecycle(st),
	})
	return err
}

func (a *Applier) fetch(ctx context.Context, url string) ([]byte, error) {
	cli := a.HTTPClient
	if cli == nil {
		cli = &http.Client{Timeout: 5 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("artifact GET %s status %d", url, resp.StatusCode)
	}
	const maxBytes = 256 * 1024 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("artifact too large (>%d bytes)", maxBytes)
	}
	return body, nil
}

func (a *Applier) emit(action, outcome string, attrs map[string]string) {
	if a.Audit == nil {
		return
	}
	a.Audit.Emit(action, outcome, attrs)
}

func (a *Applier) warn(format string, args ...any) {
	if a.Logger == nil {
		return
	}
	a.Logger.Log(logger.Warn, format, args...)
}

func stateLifecycle(s *State) string {
	if s == nil {
		return ""
	}
	return s.LastAppliedLifecycleID
}

// backupAndSwap moves binaryPath to binaryPath+".bak" and writes the
// new bytes to binaryPath via temp file + rename. fsync()s along the
// way so a crash mid-swap leaves the backup intact.
func backupAndSwap(binaryPath string, newBytes []byte) error {
	bak := binaryPath + ".bak"
	// If bak exists from a prior aborted swap, remove it first.
	if err := os.Remove(bak); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale backup: %w", err)
	}
	// Backup current binary (if it exists). On a recorder process the
	// running binary is read-mappable; copy is safe and avoids the
	// "rename a running executable" pitfall on some platforms.
	if _, err := os.Stat(binaryPath); err == nil {
		if err := copyFile(binaryPath, bak); err != nil {
			return fmt.Errorf("backup binary: %w", err)
		}
	}
	// Write new binary to a temp file in the same dir, then rename.
	dir := filepath.Dir(binaryPath)
	tmp, err := os.CreateTemp(dir, ".swap-")
	if err != nil {
		return fmt.Errorf("open tmp swap: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(newBytes); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write tmp swap: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync tmp swap: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod tmp swap: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp swap: %w", err)
	}
	if err := os.Rename(tmpPath, binaryPath); err != nil {
		return fmt.Errorf("rename tmp swap: %w", err)
	}
	return nil
}

// restoreBackup swaps binaryPath+".bak" back into binaryPath. Used
// by the post-restart rollback path.
func restoreBackup(binaryPath string) error {
	bak := binaryPath + ".bak"
	if _, err := os.Stat(bak); err != nil {
		return fmt.Errorf("backup not present at %s: %w", bak, err)
	}
	tmp := binaryPath + ".restoring"
	if err := copyFile(bak, tmp); err != nil {
		return fmt.Errorf("copy backup: %w", err)
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("chmod restore: %w", err)
	}
	if err := os.Rename(tmp, binaryPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename restore: %w", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
