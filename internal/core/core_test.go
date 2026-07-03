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
		return New([]string{})
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

	// Pre-pairing auth slice 2026-05-06 dropped the bundled mediamtx.yml's
	// 127.0.0.1-restricted internal-auth grant for api/metrics/pprof —
	// production callers now authenticate via JWT (recorder-local from
	// /v1/recorder/login or MS-issued on paired recorders). The core
	// test suite predates that slice and reaches /v1 directly without
	// minting a JWT first; restore the legacy localhost grant in the
	// test conf so those tests keep passing without bulk modification.
	// Production defaults are untouched.
	if !strings.Contains(conf, "authInternalUsers:") {
		conf = "authInternalUsers:\n" +
			"- user: any\n" +
			"  pass: \"\"\n" +
			"  permissions:\n" +
			"  - {action: publish}\n" +
			"  - {action: read}\n" +
			"  - {action: playback}\n" +
			"- user: any\n" +
			"  pass: \"\"\n" +
			"  ips: [127.0.0.1/32, ::1/128]\n" +
			"  permissions:\n" +
			"  - {action: api}\n" +
			"  - {action: metrics}\n" +
			"  - {action: pprof}\n" +
			conf
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
		
			"apiEncryption: no\n"+
			"apiServerKey: server.key\n"+
			"apiServerCert: server.crt\n"+
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
		
			"apiEncryption: no\n"+
			"apiServerKey: server.key\n"+
			"apiServerCert: server.crt\n"+
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

	err := os.WriteFile(confPath, []byte(
		"apiEncryption: no\n"+
			"apiServerKey: server.key\n"+
			"apiServerCert: server.crt\n"),
		0o644)
	require.NoError(t, err)
	defer os.Remove(confPath)

	p, ok := New([]string{confPath})
	require.Equal(t, true, ok)
	defer p.Close()

	err = os.WriteFile(confPath, []byte(
		
			"apiEncryption: no\n"+
			"apiServerKey: server.key\n"+
			"apiServerCert: server.crt\n"+
			"logDestinations: [file]\n"+
			"logFile: /nonexisting/nonexist\n"),
		0o644)
	require.NoError(t, err)

	p.Wait()
}
