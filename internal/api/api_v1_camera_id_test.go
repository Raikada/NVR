package api //nolint:revive

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
)

// TestResolveCameraPath covers the configured-first / runtime-fallback
// helper used by the recorder-localized escape-hatch handlers
// (/v1/recorder/hls-muxers/{id} and /v1/recorder/cameras/{id}/snapshot).
//
// The cases match the behavior the call sites previously implemented
// inline: configured-hit short-circuits the runtime call; on configured-
// miss the runtime muxer list is consulted; nil c.Paths or nil hlsServer
// are tolerated (no panic, miss).
func TestResolveCameraPath(t *testing.T) {
	const configuredName = "cam_configured"
	const runtimeName = "cam_runtime"
	configuredID := cameraIDFromPathName(configuredName)
	runtimeID := cameraIDFromPathName(runtimeName)

	cWithPath := &conf.Conf{
		Paths: map[string]*conf.Path{
			configuredName: {Name: configuredName},
		},
	}
	cEmpty := &conf.Conf{Paths: map[string]*conf.Path{}}

	hlsWithMuxer := &hlsMuxerOnlyServer{
		muxers: map[string]*defs.APIHLSMuxer{
			runtimeName: {Path: runtimeName},
		},
	}

	t.Run("configured hit", func(t *testing.T) {
		// Configured paths win even when a runtime muxer also exists.
		// Both contain different cameras here; the configured lookup
		// finds its match without consulting runtime.
		name, ok := resolveCameraPath(cWithPath, hlsWithMuxer, configuredID)
		require.True(t, ok)
		require.Equal(t, configuredName, name)
	})

	t.Run("runtime hit (configured miss falls through)", func(t *testing.T) {
		name, ok := resolveCameraPath(cWithPath, hlsWithMuxer, runtimeID)
		require.True(t, ok)
		require.Equal(t, runtimeName, name)
	})

	t.Run("miss in both", func(t *testing.T) {
		name, ok := resolveCameraPath(cWithPath, hlsWithMuxer,
			cameraIDFromPathName("not_present"))
		require.False(t, ok)
		require.Equal(t, "", name)
	})

	t.Run("nil c.Paths via empty conf", func(t *testing.T) {
		// c is non-nil but Paths is empty (or nil): configured leg
		// finds nothing, fallback resolves the runtime muxer.
		name, ok := resolveCameraPath(cEmpty, hlsWithMuxer, runtimeID)
		require.True(t, ok)
		require.Equal(t, runtimeName, name)
	})

	t.Run("nil conf entirely", func(t *testing.T) {
		// nil *conf.Conf must not panic; configured leg short-circuits,
		// runtime leg still runs.
		name, ok := resolveCameraPath(nil, hlsWithMuxer, runtimeID)
		require.True(t, ok)
		require.Equal(t, runtimeName, name)
	})

	t.Run("nil hlsServer", func(t *testing.T) {
		// Configured-hit still works with a nil hlsServer.
		name, ok := resolveCameraPath(cWithPath, nil, configuredID)
		require.True(t, ok)
		require.Equal(t, configuredName, name)

		// Configured-miss with nil hlsServer: helper returns miss
		// without panicking.
		name, ok = resolveCameraPath(cWithPath, nil, runtimeID)
		require.False(t, ok)
		require.Equal(t, "", name)
	})

	t.Run("nil conf and nil hlsServer", func(t *testing.T) {
		// Both legs absent: clean miss, no panic.
		name, ok := resolveCameraPath(nil, nil, configuredID)
		require.False(t, ok)
		require.Equal(t, "", name)
	})
}
