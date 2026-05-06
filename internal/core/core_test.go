package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/mediamtx/internal/test"
	"github.com/stretchr/testify/require"
)

func newInstance(conf string) (*Core, bool) {
	if conf == "" {
		// tenantId is required by Conf.Validate (D1 in
		// canonical-divergences.md); empty conf path means New()
		// loads defaults, which still need a tenantId. The CLI test
		// path Conf-loads a default file; that file must include
		// tenantId. In test isolation (the common case), pass an
		// explicit conf string instead.
		return New([]string{})
	}

	// Inject a sentinel tenantId for tests that don't set one (most).
	if !strings.Contains(conf, "tenantId:") {
		conf = "tenantId: 00000000-0000-0000-0000-000000000000\n" + conf
	}

	// Disable API encryption + use the legacy server.key/server.crt paths
	// for tests that don't explicitly set them. The production default
	// (per the bundled mediamtx.yml) is `apiEncryption: yes` with
	// identity/recorder.key paths that the identity package generates at
	// first run; tests don't run that bootstrap and don't need TLS on
	// the API. Tests that DO want encrypted API can set the fields
	// themselves and skip this block.
	if !strings.Contains(conf, "apiEncryption:") {
		conf = "apiEncryption: no\napiServerKey: server.key\napiServerCert: server.crt\n" + conf
	}

	tmpf, err := test.CreateTempFile([]byte(conf))
	if err != nil {
		return nil, false
	}
	defer os.Remove(tmpf)

	return New([]string{tmpf})
}

func TestCoreErrors(t *testing.T) {
	for _, ca := range []struct {
		name string
		conf string
	}{
		{
			"logger",
			"logDestinations: [file]\n" +
				"logFile: /nonexisting/nonexist\n" +
				"sysLogPrefix: /mediamtx\n",
		},
		{
			"metrics",
			"metrics: yes\n" +
				"metricsAddress: invalid\n",
		},
		{
			"pprof",
			"pprof: yes\n" +
				"pprofAddress: invalid\n",
		},
		{
			"playback",
			"playback: yes\n" +
				"playbackAddress: invalid\n",
		},
		{
			"rtsp",
			"rtspAddress: invalid\n",
		},
		{
			"rtsps",
			"rtspEncryption: strict\n" +
				"rtspAddress: invalid\n",
		},
		{
			"rtmp",
			"rtmpAddress: invalid\n",
		},
		{
			"rtmps",
			"rtmpEncryption: strict\n" +
				"rtmpAddress: invalid\n",
		},
		{
			"hls",
			"hlsAddress: invalid\n",
		},
		{
			"webrtc",
			"webrtcAddress: invalid\n",
		},
		{
			"srt",
			"srtAddress: invalid\n",
		},
		{
			"api",
			"api: yes\n" +
				"apiAddress: invalid\n",
		},
	} {
		t.Run(ca.name, func(t *testing.T) {
			_, ok := newInstance(ca.conf)
			require.Equal(t, false, ok)
		})
	}
}

func TestCoreHotReloading(t *testing.T) {
	confPath := filepath.Join(os.TempDir(), "rtsp-conf")

	err := os.WriteFile(confPath, []byte(
		"tenantId: 00000000-0000-0000-0000-000000000000\n"+
			"paths:\n"+
			"  test1:\n"+
			"    publishUser: myuser\n"+
			"    publishPass: mypass\n"),
		0o644)
	require.NoError(t, err)
	defer os.Remove(confPath)

	p, ok := New([]string{confPath})
	require.Equal(t, true, ok)
	defer p.Close()

	func() {
		c := gortsplib.Client{}
		err = c.StartRecording("rtsp://localhost:8554/test1",
			&description.Session{Medias: []*description.Media{test.UniqueMediaH264()}})
		require.EqualError(t, err, "bad status code: 401 (Unauthorized)")
	}()

	err = os.WriteFile(confPath, []byte(
		"tenantId: 00000000-0000-0000-0000-000000000000\n"+
			"paths:\n"+
			"  test1:\n"),
		0o644)
	require.NoError(t, err)

	time.Sleep(1 * time.Second)

	func() {
		conn := gortsplib.Client{}
		err = conn.StartRecording("rtsp://localhost:8554/test1",
			&description.Session{Medias: []*description.Media{test.UniqueMediaH264()}})
		require.NoError(t, err)
		defer conn.Close()
	}()
}

func TestCoreHotReloadingAndLoggerError(t *testing.T) {
	confPath := filepath.Join(os.TempDir(), "rtsp-conf")

	// tenantId is required by Conf.Validate() per the D1 enforcement
	// added in commit e97ba172. Without it, New() rejects the config
	// and the test gets a (nil, false) return.
	err := os.WriteFile(confPath, []byte(
		"tenantId: 00000000-0000-0000-0000-000000000000\n"),
		0o644)
	require.NoError(t, err)
	defer os.Remove(confPath)

	p, ok := New([]string{confPath})
	require.Equal(t, true, ok)
	defer p.Close()

	err = os.WriteFile(confPath, []byte(
		"tenantId: 00000000-0000-0000-0000-000000000000\n"+
			"logDestinations: [file]\n"+
			"logFile: /nonexisting/nonexist\n"),
		0o644)
	require.NoError(t, err)

	p.Wait()
}
