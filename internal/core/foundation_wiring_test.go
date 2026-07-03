package core

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/cameras"
	"github.com/bluenviron/mediamtx/internal/conf"
)

// A camera added after boot has no entry in the adapter's boot-time
// defaults; the synthesized path must inherit real per-path defaults
// (not a zero-value conf.Path, whose empty RTSPUDPSourcePortRange
// panics the RTSP dialer).
func TestCameraSpecToConfPathNilBaseUsesTemplate(t *testing.T) {
	var tmpl conf.Path
	tmpl.SetDefaults()

	p := cameraSpecToConfPath(cameras.CameraPathSpec{
		Name:      "post_boot_cam",
		SourceURL: "rtsp://u:p@192.0.2.1:554/stream",
	}, nil, &tmpl)

	require.Equal(t, "post_boot_cam", p.Name)
	require.Equal(t, "rtsp://u:p@192.0.2.1:554/stream", p.Source)
	require.Len(t, p.RTSPUDPSourcePortRange, 2)
}

// An operator-supplied base path must keep taking precedence over the
// template so customized recording fields survive credential rotations.
func TestCameraSpecToConfPathBasePreserved(t *testing.T) {
	var tmpl conf.Path
	tmpl.SetDefaults()

	base := &conf.Path{RecordPath: "/custom/%path/%s"}
	base.SetDefaults()
	base.RecordPath = "/custom/%path/%s"

	p := cameraSpecToConfPath(cameras.CameraPathSpec{
		Name:      "cam1",
		SourceURL: "rtsp://u:p@192.0.2.1:554/stream",
	}, base, &tmpl)

	require.Equal(t, "/custom/%path/%s", p.RecordPath)
	require.Equal(t, "rtsp://u:p@192.0.2.1:554/stream", p.Source)
}

// RefreshDefaults must fold paths created after boot (e.g. via
// POST /v1/cameras + APIConfigSet) into the adapter, so a subsequent
// credential rotation merges against the real path conf instead of a
// nil base.
func TestPathManagerAdapterRefreshDefaults(t *testing.T) {
	bootPath := &conf.Path{}
	bootPath.SetDefaults()
	bootPath.Name = "boot_cam"

	a := newPathManagerAdapter(nil, map[string]*conf.Path{"boot_cam": bootPath}, nil)

	newPath := &conf.Path{}
	newPath.SetDefaults()
	newPath.Name = "post_boot_cam"
	newPath.RecordPath = "/from/api/%path/%s"

	c := &conf.Conf{Paths: map[string]*conf.Path{
		"boot_cam":      bootPath,
		"post_boot_cam": newPath,
	}}
	c.PathDefaults.SetDefaults()

	a.RefreshDefaults(c)

	base, tmpl := a.baseFor("post_boot_cam")
	require.NotNil(t, base)
	require.Equal(t, "/from/api/%path/%s", base.RecordPath)
	require.NotNil(t, tmpl)
	require.Len(t, tmpl.RTSPUDPSourcePortRange, 2)
}
