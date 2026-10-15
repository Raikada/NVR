package playback

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
)

// A camera that has never recorded has no per-path directory yet;
// coverage must report "no footage", not a 500 (Phase A live-verify
// finding).
func TestCoverageMissingDirIsEmpty(t *testing.T) {
	pathConf := &conf.Path{
		RecordPath:   filepath.Join(t.TempDir(), "nonexistent", "%path", "%Y-%m-%d_%H-%M-%S-%f"),
		RecordFormat: conf.RecordFormatFMP4,
	}
	entries, err := Coverage(pathConf, "nevercam", time.Time{}, time.Time{})
	require.NoError(t, err)
	require.Empty(t, entries)
}
