package recordcleaner

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/test"
	"github.com/stretchr/testify/require"
)

func TestCleaner(t *testing.T) {
	timeNow = func() time.Time {
		return time.Date(2009, 5, 20, 22, 15, 25, 427000, time.Local)
	}

	dir, err := os.MkdirTemp("", "mediamtx-cleaner")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	const specialChars = "_-+*?^$()[]{}|"

	err = os.Mkdir(filepath.Join(dir, specialChars+"_mypath"), 0o755)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(dir, specialChars+"_mypath", "2008-05-20_22-15-25-000125.mp4"), []byte{1}, 0o644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(dir, specialChars+"_mypath", "2009-05-20_22-15-25-000427.mp4"), []byte{1}, 0o644)
	require.NoError(t, err)

	c := &Cleaner{
		PathConfs: map[string]*conf.Path{
			"~^.*$": {
				Name:              "~^.*$",
				Regexp:            regexp.MustCompile("^.*$"),
				RecordPath:        filepath.Join(dir, specialChars+"_%path/%Y-%m-%d_%H-%M-%S-%f"),
				RecordFormat:      conf.RecordFormatFMP4,
				RecordDeleteAfter: conf.Duration(10 * time.Second),
			},
		},
		Parent: test.NilLogger,
	}
	c.Initialize()
	defer c.Close()

	time.Sleep(500 * time.Millisecond)

	_, err = os.Stat(filepath.Join(dir, specialChars+"_mypath", "2008-05-20_22-15-25-000125.mp4"))
	require.Error(t, err)

	_, err = os.Stat(filepath.Join(dir, specialChars+"_mypath", "2009-05-20_22-15-25-000427.mp4"))
	require.NoError(t, err)
}

// TestCleanerSkipsPinnedSegments verifies that the SegmentPinPredicate
// hook (set by internal/api when a Clip is in
// requested/preparing/ready state) prevents the cleaner from removing
// a segment that would otherwise be expired by retention.
//
// Per platform/docs/domain-model.md: "the recorder MUST NOT prune any
// RecordingSegment referenced by a clip whose state is requested,
// preparing, or ready." This test exercises that contract at the
// cleaner level by registering a predicate inline.
func TestCleanerSkipsPinnedSegments(t *testing.T) {
	timeNow = func() time.Time {
		return time.Date(2009, 5, 20, 22, 15, 25, 427000, time.Local)
	}

	dir, err := os.MkdirTemp("", "mediamtx-cleaner-pin")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	require.NoError(t, os.Mkdir(filepath.Join(dir, "cam_pinned"), 0o755))

	pinnedSeg := filepath.Join(dir, "cam_pinned", "2008-05-20_22-15-25-000125.mp4")
	otherSeg := filepath.Join(dir, "cam_pinned", "2008-05-21_22-15-25-000125.mp4")
	require.NoError(t, os.WriteFile(pinnedSeg, []byte{1}, 0o644))
	require.NoError(t, os.WriteFile(otherSeg, []byte{1}, 0o644))

	// Resolve the absolute path the cleaner will see (FindSegments
	// runs filepath.Abs internally; the predicate is invoked with the
	// returned Fpath, which is the abs form).
	absPinned, err := filepath.Abs(pinnedSeg)
	require.NoError(t, err)

	prev := SegmentPinPredicate
	SegmentPinPredicate = func(p string) bool {
		return p == absPinned
	}
	defer func() { SegmentPinPredicate = prev }()

	c := &Cleaner{
		PathConfs: map[string]*conf.Path{
			"cam_pinned": {
				Name:              "cam_pinned",
				RecordPath:        filepath.Join(dir, "%path/%Y-%m-%d_%H-%M-%S-%f"),
				RecordFormat:      conf.RecordFormatFMP4,
				RecordDeleteAfter: conf.Duration(10 * time.Second),
			},
		},
		Parent: test.NilLogger,
	}
	c.Initialize()
	defer c.Close()

	time.Sleep(500 * time.Millisecond)

	// Pinned segment must survive even though retention has expired.
	_, err = os.Stat(pinnedSeg)
	require.NoError(t, err, "pinned segment must not be deleted by cleaner")

	// Non-pinned, similarly-expired segment must be deleted.
	_, err = os.Stat(otherSeg)
	require.Error(t, err, "non-pinned expired segment must be deleted")
}

func TestCleanerMultipleEntriesSamePath(t *testing.T) {
	timeNow = func() time.Time {
		return time.Date(2009, 5, 20, 22, 15, 25, 427000, time.Local)
	}

	dir, err := os.MkdirTemp("", "mediamtx-cleaner")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	err = os.Mkdir(filepath.Join(dir, "path1"), 0o755)
	require.NoError(t, err)

	err = os.Mkdir(filepath.Join(dir, "path2"), 0o755)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(dir, "path1", "2009-05-19_22-15-25-000427.mp4"), []byte{1}, 0o644)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(dir, "path2", "2009-05-19_22-15-25-000427.mp4"), []byte{1}, 0o644)
	require.NoError(t, err)

	c := &Cleaner{
		PathConfs: map[string]*conf.Path{
			"path1": {
				Name:              "path1",
				RecordPath:        filepath.Join(dir, "%path/%Y-%m-%d_%H-%M-%S-%f"),
				RecordFormat:      conf.RecordFormatFMP4,
				RecordDeleteAfter: conf.Duration(10 * time.Second),
			},
			"path2": {
				Name:              "path2",
				RecordPath:        filepath.Join(dir, "%path/%Y-%m-%d_%H-%M-%S-%f"),
				RecordFormat:      conf.RecordFormatFMP4,
				RecordDeleteAfter: conf.Duration(10 * 24 * time.Hour),
			},
		},
		Parent: test.NilLogger,
	}
	c.Initialize()
	defer c.Close()

	time.Sleep(500 * time.Millisecond)

	_, err = os.Stat(filepath.Join(dir, "path1", "2009-05-19_22-15-25-000427.mp4"))
	require.Error(t, err)

	_, err = os.Stat(filepath.Join(dir, "path1"))
	require.Error(t, err, "testing")

	_, err = os.Stat(filepath.Join(dir, "path2", "2009-05-19_22-15-25-000427.mp4"))
	require.NoError(t, err)
}
