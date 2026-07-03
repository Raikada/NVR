// Package api: SP4 follow-up — sample-accurate clip trimming tests.
package api //nolint:revive

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBuildTrimPlan(t *testing.T) {
	base := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	segs := []segmentLookup{
		{started: base},                        // covers 12:00:00 onward
		{started: base.Add(10 * time.Minute)},  // 12:10:00
		{started: base.Add(20 * time.Minute)},  // 12:20:00
	}
	// Clip window 12:05:30 → 12:25:15.
	plan := buildTrimPlan(segs, base.Add(5*time.Minute+30*time.Second), base.Add(25*time.Minute+15*time.Second))
	require.Len(t, plan, 3)

	// First segment: skip 5m30s in, keep the rest.
	require.Equal(t, 5*time.Minute+30*time.Second, plan[0].SkipBefore)
	require.Equal(t, 25*time.Minute+15*time.Second, plan[0].DropAfter)
	// Middle segment: no leading skip; drop-after beyond its content.
	require.Equal(t, time.Duration(0), plan[1].SkipBefore)
	require.Equal(t, 15*time.Minute+15*time.Second, plan[1].DropAfter)
	// Last segment: keep only the first 5m15s.
	require.Equal(t, time.Duration(0), plan[2].SkipBefore)
	require.Equal(t, 5*time.Minute+15*time.Second, plan[2].DropAfter)
}

// probeDurationSeconds opens an mp4 and returns its container duration.
func probeDurationSeconds(t *testing.T, path string) float64 {
	t.Helper()
	fc, err := openInputSegment(path)
	require.NoError(t, err)
	defer func() {
		fc.CloseInput()
		fc.Free()
	}()
	return float64(fc.Duration()) / 1e6 // AV_TIME_BASE microseconds
}

// Trimming with DropAfter shorter than the content must shorten the
// output; a full-width trim window must be a no-op.
func TestRemuxTrimDropAfterShortensOutput(t *testing.T) {
	dir := t.TempDir()
	seg := filepath.Join(dir, "seg.mp4")
	require.NoError(t, os.WriteFile(seg, fixtureMP4Bytes(t), 0o644))

	fullOut := filepath.Join(dir, "full.mp4")
	_, _, err := remuxFmp4Segments([]string{seg}, fullOut, nil)
	require.NoError(t, err)
	fullDur := probeDurationSeconds(t, fullOut)
	require.Greater(t, fullDur, 0.5, "fixture must be long enough to trim")

	// Keep only the first 40% of the fixture.
	cut := time.Duration(fullDur * 0.4 * float64(time.Second))
	trimOut := filepath.Join(dir, "trim.mp4")
	_, _, err = remuxFmp4Segments([]string{seg}, trimOut,
		[]segmentTrim{{SkipBefore: 0, DropAfter: cut}})
	require.NoError(t, err)
	trimDur := probeDurationSeconds(t, trimOut)
	require.Less(t, trimDur, fullDur*0.75, "trimmed output must be meaningfully shorter (got %.2fs of %.2fs)", trimDur, fullDur)

	// A window wider than the content is a no-op.
	wideOut := filepath.Join(dir, "wide.mp4")
	_, _, err = remuxFmp4Segments([]string{seg}, wideOut,
		[]segmentTrim{{SkipBefore: 0, DropAfter: time.Hour}})
	require.NoError(t, err)
	require.InDelta(t, fullDur, probeDurationSeconds(t, wideOut), 0.15)
}
