// Package api: snapshot JPEG conversion via system ffmpeg.
//
// The /v1/recorder/cameras/{id}/snapshot endpoint's first cut returned
// raw HLS-fragment bytes — technically functional but not fit for the
// typical "thumbnail in a UI" or "frame-as-evidence" use cases, since
// fMP4 fragments without their init segment are undecodable and even
// MPEG-TS fragments are short-video, not still-frame.
//
// To produce real JPEG output without pulling in a cgo+libav
// dependency, the recorder shells out to a system `ffmpeg` binary
// when one is available on PATH. The pattern matches the recorder's
// existing `runOn*` external-command hooks: an external binary at
// runtime, no build-time toolchain change.
//
// When ffmpeg is not available the snapshot endpoint falls back to
// the raw fragment bytes (the original behavior), with a header noting
// the format. Operators who want JPEG install ffmpeg on the recorder
// host; everyone else gets the previous contract.
package api //nolint:revive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// snapshotJPEGTimeout caps the time the recorder will wait for an
// ffmpeg invocation to complete. ffmpeg processes a single-fragment
// keyframe extract in tens of milliseconds on commodity hardware; a
// few-second cap accommodates large frames or system load without
// letting a hung subprocess pile up.
var snapshotJPEGTimeout = 5 * time.Second

// snapshotFFmpegOnce + snapshotFFmpegPath cache the result of the one-
// shot ffmpeg lookup. exec.LookPath touches PATH; doing it once at
// first call (rather than on every request) keeps the snapshot path
// cheap and avoids racing against PATH mutations during process
// lifetime.
var (
	snapshotFFmpegOnce sync.Once
	snapshotFFmpegPath string
)

// snapshotFFmpegLookup is the function that resolves ffmpeg's path.
// Defaults to exec.LookPath; tests override to simulate
// available/unavailable ffmpeg without touching the real PATH.
var snapshotFFmpegLookup = func() (string, error) {
	return exec.LookPath("ffmpeg")
}

// resolveFFmpeg returns the cached ffmpeg path. Empty string means
// ffmpeg is not available on this host; callers should fall back to
// raw fragment bytes.
func resolveFFmpeg() string {
	snapshotFFmpegOnce.Do(func() {
		path, err := snapshotFFmpegLookup()
		if err != nil {
			snapshotFFmpegPath = ""
			return
		}
		snapshotFFmpegPath = path
	})
	return snapshotFFmpegPath
}

// errFFmpegUnavailable signals that ffmpeg is not on the recorder's
// PATH. Callers map this to the "fall back to raw fragment bytes"
// branch; other errors (ffmpeg present but failed to convert) are
// logged but also fall back so the snapshot endpoint stays functional
// even when conversion fails.
var errFFmpegUnavailable = errors.New("ffmpeg not available on PATH")

// snapshotJPEGFromFragment invokes the system ffmpeg binary to convert
// a single HLS fragment (or init+fragment for fMP4 variants) into a
// JPEG-encoded keyframe. Returns errFFmpegUnavailable when no ffmpeg
// is on PATH; returns a wrapped exec error when ffmpeg ran but failed.
//
// Implementation note: ffmpeg works substantially better with a
// seekable on-disk input than with a piped stdin, particularly for
// fMP4 inputs where it needs to read the moov box before processing
// moof/mdat. We write the fragment to a temp file, invoke ffmpeg with
// that as input, and read JPEG bytes from stdout. The temp file is
// removed before return regardless of outcome.
func snapshotJPEGFromFragment(ctx context.Context, fragment []byte) ([]byte, error) {
	if len(fragment) == 0 {
		return nil, fmt.Errorf("snapshot fragment is empty")
	}

	ffmpegPath := resolveFFmpeg()
	if ffmpegPath == "" {
		return nil, errFFmpegUnavailable
	}

	tmp, err := os.CreateTemp("", "raikada-snapshot-*.bin")
	if err != nil {
		return nil, fmt.Errorf("creating temp fragment file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(fragment); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("writing temp fragment file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("closing temp fragment file: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, snapshotJPEGTimeout)
	defer cancel()

	// Flags:
	//   -loglevel error  → ffmpeg writes only errors to stderr; we capture
	//                      them for the wrapped error message.
	//   -i <tmp>         → input file.
	//   -frames:v 1      → output exactly one video frame.
	//   -f mjpeg pipe:1  → emit MJPEG-format JPEG bytes to stdout.
	cmd := exec.CommandContext(runCtx, ffmpegPath,
		"-loglevel", "error",
		"-i", tmpName,
		"-frames:v", "1",
		"-f", "mjpeg",
		"pipe:1",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ffmpeg produced empty output (stderr: %s)", strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
