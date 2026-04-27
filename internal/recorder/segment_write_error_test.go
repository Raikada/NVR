package recorder

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSegmentWriteError_WrapAndUnwrap verifies the basic wrap /
// errors.As / errors.Is contract that recorder_instance.run() uses
// to decide whether to emit segment.write_failed.
func TestSegmentWriteError_WrapAndUnwrap(t *testing.T) {
	inner := errors.New("disk full")

	wrapped := wrapFMP4WriteErr("/srv/rec/cam1/2026-04-26.mp4", inner)
	require.Error(t, wrapped)
	require.Equal(t, "disk full", wrapped.Error())

	var swe *SegmentWriteError
	require.True(t, errors.As(wrapped, &swe))
	require.Equal(t, "fmp4", swe.Format)
	require.Equal(t, "/srv/rec/cam1/2026-04-26.mp4", swe.Path)
	require.True(t, errors.Is(wrapped, inner))

	// MPEG-TS variant.
	wrapped2 := wrapMPEGTSWriteErr("/srv/rec/cam2/x.ts", inner)
	var swe2 *SegmentWriteError
	require.True(t, errors.As(wrapped2, &swe2))
	require.Equal(t, "mpegts", swe2.Format)

	// nil propagates as nil — wrap helpers must not allocate when
	// the underlying op succeeded.
	require.NoError(t, wrapFMP4WriteErr("p", nil))
	require.NoError(t, wrapMPEGTSWriteErr("p", nil))
}

// TestSegmentWriteError_DifferentiatesAtPublishSite simulates the
// errors.As decision in recorder_instance.run() to confirm only
// SegmentWriteError-class errors fire the publisher; other errors
// (network, codec) reaching the same channel do not.
func TestSegmentWriteError_DifferentiatesAtPublishSite(t *testing.T) {
	type emit struct{ pathName, segmentPath, reason string }
	var emits []emit

	SetSegmentWriteFailedPublisher(func(pathName, segmentPath, reason string) {
		emits = append(emits, emit{pathName, segmentPath, reason})
	})
	t.Cleanup(func() { SetSegmentWriteFailedPublisher(nil) })

	// Helper that mirrors the differentiation block in run().
	dispatch := func(pathName string, err error) {
		var swe *SegmentWriteError
		if errors.As(err, &swe) && segmentWriteFailedPublisher != nil {
			reason := ""
			if swe.Inner != nil {
				reason = swe.Inner.Error()
			}
			segmentWriteFailedPublisher(pathName, swe.Path, reason)
		}
	}

	// Write failure → emits.
	dispatch("cam1", wrapFMP4WriteErr("/srv/rec/cam1/seg.mp4", fmt.Errorf("ENOSPC")))
	// Wrapped at a higher layer (errors.As must still find it).
	dispatch("cam1", fmt.Errorf("during flush: %w",
		wrapMPEGTSWriteErr("/srv/rec/cam1/seg.ts", fmt.Errorf("EACCES"))))

	// Non-write failures must NOT emit.
	dispatch("cam1", errors.New("network read failed: connection reset"))
	dispatch("cam1", fmt.Errorf("invalid AC-3 frame: bad sync"))
	dispatch("cam1", fmt.Errorf("detected drift between recording duration and absolute time, resetting"))

	require.Len(t, emits, 2)
	require.Equal(t, "cam1", emits[0].pathName)
	require.Equal(t, "/srv/rec/cam1/seg.mp4", emits[0].segmentPath)
	require.Equal(t, "ENOSPC", emits[0].reason)
	require.Equal(t, "/srv/rec/cam1/seg.ts", emits[1].segmentPath)
	require.Equal(t, "EACCES", emits[1].reason)
}
