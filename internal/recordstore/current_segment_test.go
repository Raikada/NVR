package recordstore

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCurrentSegmentsRegisterLookupUnregister(t *testing.T) {
	c := &CurrentSegments{}

	require.False(t, c.IsActive("/tmp/seg1.mp4"))

	c.Register("/tmp/seg1.mp4")
	require.True(t, c.IsActive("/tmp/seg1.mp4"))
	require.False(t, c.IsActive("/tmp/seg2.mp4"))

	c.Register("/tmp/seg2.mp4")
	require.True(t, c.IsActive("/tmp/seg1.mp4"))
	require.True(t, c.IsActive("/tmp/seg2.mp4"))

	snap := c.Snapshot()
	require.ElementsMatch(t, []string{"/tmp/seg1.mp4", "/tmp/seg2.mp4"}, snap)

	c.Unregister("/tmp/seg1.mp4")
	require.False(t, c.IsActive("/tmp/seg1.mp4"))
	require.True(t, c.IsActive("/tmp/seg2.mp4"))

	c.Unregister("/tmp/seg2.mp4")
	require.False(t, c.IsActive("/tmp/seg2.mp4"))
	require.Empty(t, c.Snapshot())
}

func TestCurrentSegmentsUnregisterMissingIsNoop(t *testing.T) {
	c := &CurrentSegments{}
	// Must not panic on a never-registered path.
	c.Unregister("/tmp/never-registered.mp4")
	require.False(t, c.IsActive("/tmp/never-registered.mp4"))
}

func TestCurrentSegmentsConcurrent(t *testing.T) {
	c := &CurrentSegments{}
	var wg sync.WaitGroup
	const n = 50

	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			c.Register(fpathFor(i))
		}(i)
		go func(i int) {
			defer wg.Done()
			_ = c.IsActive(fpathFor(i))
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		require.True(t, c.IsActive(fpathFor(i)))
	}

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.Unregister(fpathFor(i))
		}(i)
	}
	wg.Wait()
	require.Empty(t, c.Snapshot())
}

func fpathFor(i int) string {
	return "/tmp/seg-" + itoa(i) + ".mp4"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

func TestPackageLevelRegistryRoundtrip(t *testing.T) {
	const fpath = "/tmp/pkg-level-seg.mp4"
	// Defensive: in case a prior test or production code in the same
	// process registered something, we only care about our own key.
	require.False(t, IsCurrentSegment(fpath))

	RegisterCurrentSegment(fpath)
	require.True(t, IsCurrentSegment(fpath))

	UnregisterCurrentSegment(fpath)
	require.False(t, IsCurrentSegment(fpath))
}
