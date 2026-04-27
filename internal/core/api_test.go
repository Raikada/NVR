//nolint:dupl,lll
package core

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/bluenviron/gohlslib/v2"
	"github.com/bluenviron/gortmplib"
	rtmpcodecs "github.com/bluenviron/gortmplib/pkg/codecs"
	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts"
	tscodecs "github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts/codecs"
	srt "github.com/datarhei/gosrt"
	"github.com/google/uuid"
	"github.com/pion/rtp"
	pwebrtc "github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/protocols/webrtc"
	"github.com/bluenviron/mediamtx/internal/protocols/whip"
	"github.com/bluenviron/mediamtx/internal/test"
)

func checkClose(t *testing.T, closeFunc func() error) {
	require.NoError(t, closeFunc())
}

func httpRequest(t *testing.T, hc *http.Client, method string, ur string, in any, out any) {
	buf := func() io.Reader {
		if in == nil {
			return nil
		}

		byts, err := json.Marshal(in)
		require.NoError(t, err)

		return bytes.NewBuffer(byts)
	}()

	req, err := http.NewRequest(method, ur, buf)
	require.NoError(t, err)

	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	// Accept any 2xx — POST /v1/cameras returns 201 Created per REST
	// convention (ADR 0009), and other future canonical endpoints may
	// return 202/204 as appropriate.
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		t.Errorf("bad status code: %d", res.StatusCode)
	}

	if out == nil {
		return
	}

	err = json.NewDecoder(res.Body).Decode(out)
	require.NoError(t, err)
}

func checkError(t *testing.T, msg string, body io.Reader) {
	var resErr map[string]any
	err := json.NewDecoder(body).Decode(&resErr)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"status": "error", "error": msg}, resErr)
}

// cameraIDFromPathName mirrors internal/api.cameraIDFromPathName: a
// deterministic UUIDv5 (NameSpaceOID, path-name) so tests can address
// cameras by canonical id without depending on internal API helpers.
// Empty input returns "" (matches the api helper's empty-input guard
// from commit 112c9889). Keep in sync with internal/api/api_v1_camera_id.go.
func cameraIDFromPathName(name string) string {
	if name == "" {
		return ""
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)).String()
}

// TestAPIPathsList verifies that configured cameras appear at /v1/cameras
// with the canonical Camera shape, and that a publishing session shows up
// at /v1/streams with protocol-correct discrimination. The old
// /v3/paths/list returned a hybrid of camera-config and runtime status
// keyed by path name; ADR 0009 §D3 splits that into Camera (config +
// runtime block) and Stream (active sessions).
func TestAPIPathsList(t *testing.T) {
	type cameraList struct {
		ItemCount int           `json:"item_count"`
		PageCount int           `json:"page_count"`
		Items     []defs.Camera `json:"items"`
	}

	type streamList struct {
		ItemCount int           `json:"item_count"`
		PageCount int           `json:"page_count"`
		Items     []defs.Stream `json:"items"`
	}

	t.Run("rtsp session", func(t *testing.T) {
		p, ok := newInstance("api: yes\n" +
			"paths:\n" +
			"  mypath:\n")
		require.Equal(t, true, ok)
		defer p.Close()

		tr := &http.Transport{}
		defer tr.CloseIdleConnections()
		hc := &http.Client{Transport: tr}

		media0 := test.UniqueMediaH264()

		source := gortsplib.Client{}
		err := source.StartRecording(
			"rtsp://localhost:8554/mypath",
			&description.Session{Medias: []*description.Media{
				media0,
				test.MediaMPEG4Audio,
			}})
		require.NoError(t, err)
		defer source.Close()

		err = source.WritePacketRTP(media0, &rtp.Packet{
			Header: rtp.Header{
				Version:     2,
				PayloadType: 96,
			},
			Payload: []byte{5, 1, 2, 3, 4},
		})
		require.NoError(t, err)

		// Camera config: source_type publish (default conf.Path source is
		// "publisher" which maps to canonical CameraSourceTypePublish) and
		// runtime should be online because the rtsp publisher is connected.
		var cams cameraList
		httpRequest(t, hc, http.MethodGet, "http://localhost:9997/v1/cameras", nil, &cams)
		require.Equal(t, 1, cams.ItemCount)
		require.Len(t, cams.Items, 1)
		require.Equal(t, "mypath", cams.Items[0].Name)
		require.Equal(t, defs.CameraSourceTypePublish, cams.Items[0].SourceType)
		require.NotNil(t, cams.Items[0].Runtime)
		require.True(t, cams.Items[0].Runtime.Available)

		// Stream runtime: an active publishing rtsp session for camera "mypath".
		var streams streamList
		httpRequest(t, hc, http.MethodGet,
			"http://localhost:9997/v1/streams?camera_id="+cameraIDFromPathName("mypath"),
			nil, &streams)
		require.GreaterOrEqual(t, streams.ItemCount, 1)
		var sess *defs.Stream
		for i := range streams.Items {
			if streams.Items[i].Protocol == defs.StreamProtocolRTSP &&
				streams.Items[i].Direction == defs.StreamDirectionPublish {
				sess = &streams.Items[i]
				break
			}
		}
		require.NotNil(t, sess, "expected an rtsp publish stream for camera mypath")
		require.Equal(t, defs.StreamProtocolRTSP, sess.Protocol)
		require.Equal(t, defs.StreamStateActive, sess.State)
		require.Equal(t, int64(17), sess.BytesInbound)
	})

	t.Run("rtsps session", func(t *testing.T) {
		serverCertFpath, err := test.CreateTempFile(test.TLSCertPub)
		require.NoError(t, err)
		defer os.Remove(serverCertFpath)

		serverKeyFpath, err := test.CreateTempFile(test.TLSCertKey)
		require.NoError(t, err)
		defer os.Remove(serverKeyFpath)

		p, ok := newInstance("api: yes\n" +
			"rtspEncryption: optional\n" +
			"rtspServerCert: " + serverCertFpath + "\n" +
			"rtspServerKey: " + serverKeyFpath + "\n" +
			"paths:\n" +
			"  mypath:\n")
		require.Equal(t, true, ok)
		defer p.Close()

		tr := &http.Transport{}
		defer tr.CloseIdleConnections()
		hc := &http.Client{Transport: tr}

		source := gortsplib.Client{TLSConfig: &tls.Config{InsecureSkipVerify: true}}
		err = source.StartRecording("rtsps://localhost:8322/mypath",
			&description.Session{Medias: []*description.Media{
				test.UniqueMediaH264(),
				test.UniqueMediaMPEG4Audio(),
			}})
		require.NoError(t, err)
		defer source.Close()

		// Stream side: an rtsps publish session is active for camera "mypath".
		var streams streamList
		httpRequest(t, hc, http.MethodGet,
			"http://localhost:9997/v1/streams?camera_id="+cameraIDFromPathName("mypath"),
			nil, &streams)
		var sess *defs.Stream
		for i := range streams.Items {
			if streams.Items[i].Protocol == defs.StreamProtocolRTSPS &&
				streams.Items[i].Direction == defs.StreamDirectionPublish {
				sess = &streams.Items[i]
				break
			}
		}
		require.NotNil(t, sess, "expected an rtsps publish stream for camera mypath")
		require.Equal(t, defs.StreamProtocolRTSPS, sess.Protocol)
		require.Equal(t, defs.StreamStateActive, sess.State)
	})

	t.Run("rtsp source", func(t *testing.T) {
		p, ok := newInstance("api: yes\n" +
			"paths:\n" +
			"  mypath:\n" +
			"    source: rtsp://127.0.0.1:1234/mypath\n" +
			"    sourceOnDemand: yes\n")
		require.Equal(t, true, ok)
		defer p.Close()

		tr := &http.Transport{}
		defer tr.CloseIdleConnections()
		hc := &http.Client{Transport: tr}

		// On-demand rtsp source not yet connected: the camera shows up in
		// config with the canonical rtsp source_type, and runtime should
		// reflect "not online" (no publisher attached because nobody asked).
		var cams cameraList
		httpRequest(t, hc, http.MethodGet, "http://localhost:9997/v1/cameras", nil, &cams)
		require.Equal(t, 1, cams.ItemCount)
		require.Equal(t, "mypath", cams.Items[0].Name)
		require.Equal(t, defs.CameraSourceTypeRTSP, cams.Items[0].SourceType)
		// On-demand block is surfaced when source_on_demand: yes.
		require.NotNil(t, cams.Items[0].OnDemand)
		require.True(t, cams.Items[0].OnDemand.Enabled)
	})

	t.Run("rtmp source", func(t *testing.T) {
		p, ok := newInstance("api: yes\n" +
			"paths:\n" +
			"  mypath:\n" +
			"    source: rtmp://127.0.0.1:1234/mypath\n" +
			"    sourceOnDemand: yes\n")
		require.Equal(t, true, ok)
		defer p.Close()

		tr := &http.Transport{}
		defer tr.CloseIdleConnections()
		hc := &http.Client{Transport: tr}

		var cams cameraList
		httpRequest(t, hc, http.MethodGet, "http://localhost:9997/v1/cameras", nil, &cams)
		require.Equal(t, 1, cams.ItemCount)
		require.Equal(t, "mypath", cams.Items[0].Name)
		require.Equal(t, defs.CameraSourceTypeRTMP, cams.Items[0].SourceType)
	})

	t.Run("hls source", func(t *testing.T) {
		p, ok := newInstance("api: yes\n" +
			"paths:\n" +
			"  mypath:\n" +
			"    source: http://127.0.0.1:1234/mypath\n" +
			"    sourceOnDemand: yes\n")
		require.Equal(t, true, ok)
		defer p.Close()

		tr := &http.Transport{}
		defer tr.CloseIdleConnections()
		hc := &http.Client{Transport: tr}

		var cams cameraList
		httpRequest(t, hc, http.MethodGet, "http://localhost:9997/v1/cameras", nil, &cams)
		require.Equal(t, 1, cams.ItemCount)
		require.Equal(t, "mypath", cams.Items[0].Name)
		require.Equal(t, defs.CameraSourceTypeHLS, cams.Items[0].SourceType)
	})
}

// TestAPIPathsGet verifies that an active publisher on a (possibly nested)
// dynamic path-name is observable via the canonical /v1/streams surface,
// keyed by the deterministic UUIDv5 derived from the runtime path-name.
//
// The old /v3/paths/get/{name} endpoint returned a hybrid per-path config
// plus runtime status keyed by the runtime path-name (which may be a
// dynamic name matched by a wildcard config like `all_others`). ADR 0009
// §D5 splits this into:
//   - /v1/cameras/{id} for *configured* cameras (the wildcard config
//     itself is one camera; dynamic match-only path-names are not
//     individual cameras).
//   - /v1/streams[?camera_id=...] for active sessions (which use the
//     same UUIDv5 derivation off the runtime path-name).
//
// So the canonical equivalent of "is path 'mypath' currently publishing"
// is "does /v1/streams have a stream whose camera_id is UUIDv5(mypath)
// and whose direction is publish". That is what we assert here.
func TestAPIPathsGet(t *testing.T) {
	p, ok := newInstance("api: yes\n" +
		"paths:\n" +
		"  all_others:\n")
	require.Equal(t, true, ok)
	defer p.Close()

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr}

	for _, ca := range []string{"ok", "ok-nested", "not found"} {
		t.Run(ca, func(t *testing.T) {
			var pathName string

			switch ca {
			case "ok":
				pathName = "mypath"
			case "ok-nested":
				pathName = "my/nested/path"
			case "not found":
				pathName = "nonexisting"
			}

			if ca == "ok" || ca == "ok-nested" {
				source := gortsplib.Client{}
				err := source.StartRecording("rtsp://localhost:8554/"+pathName,
					&description.Session{Medias: []*description.Media{test.UniqueMediaH264()}})
				require.NoError(t, err)
				defer source.Close()

				// Active runtime: an rtsp publish stream exists for the
				// dynamic path-name. The canonical Stream's camera_id is
				// the deterministic UUIDv5 of the runtime path-name; we
				// query /v1/streams filtered by that camera_id.
				cameraID := cameraIDFromPathName(pathName)
				var streams struct {
					ItemCount int           `json:"item_count"`
					Items     []defs.Stream `json:"items"`
				}
				httpRequest(t, hc, http.MethodGet,
					"http://localhost:9997/v1/streams?camera_id="+cameraID, nil, &streams)
				var sess *defs.Stream
				for i := range streams.Items {
					if streams.Items[i].Protocol == defs.StreamProtocolRTSP &&
						streams.Items[i].Direction == defs.StreamDirectionPublish {
						sess = &streams.Items[i]
						break
					}
				}
				require.NotNil(t, sess,
					"expected an rtsp publish stream for runtime path %s", pathName)
				require.Equal(t, defs.StreamStateActive, sess.State)
			} else {
				// Unknown stream id: /v1/streams/{id} returns 404 with the
				// in-house error envelope.
				res, err := hc.Get("http://localhost:9997/v1/streams/" + uuid.New().String())
				require.NoError(t, err)
				defer res.Body.Close()

				require.Equal(t, http.StatusNotFound, res.StatusCode)
				checkError(t, "stream not found", res.Body)
			}
		})
	}
}

func TestAPIProtocolListGet(t *testing.T) {
	serverCertFpath, err := test.CreateTempFile(test.TLSCertPub)
	require.NoError(t, err)
	defer os.Remove(serverCertFpath)

	serverKeyFpath, err := test.CreateTempFile(test.TLSCertKey)
	require.NoError(t, err)
	defer os.Remove(serverKeyFpath)

	for _, ca := range []string{
		"rtsp conns",
		"rtsp sessions",
		"rtsps conns",
		"rtsps sessions",
		"rtmp",
		"rtmps",
		"hls sessions",
		"hls muxers",
		"webrtc",
		"srt",
	} {
		t.Run(ca, func(t *testing.T) {
			cnf := "api: yes\n"

			switch ca {
			case "rtsps conns", "rtsps sessions":
				cnf += "rtspEncryption: strict\n" +
					"rtspServerCert: " + serverCertFpath + "\n" +
					"rtspServerKey: " + serverKeyFpath + "\n"

			case "rtmps":
				cnf += "rtmpEncryption: strict\n" +
					"rtmpServerCert: " + serverCertFpath + "\n" +
					"rtmpServerKey: " + serverKeyFpath + "\n"
			}

			cnf += "paths:\n"
			// /v1/recorder/hls-muxers/{id} resolves the camera UUID back to a
			// path-name via the configured-paths map (per ADR 0009 §D6
			// escape-hatch shape: identifiers are canonical UUIDs even on
			// the recorder-localized endpoints). A wildcard `all_others` config
			// would not satisfy this lookup for the dynamic "mypath" the
			// publisher creates, so we explicitly configure the path for this
			// case. Other cases keep the wildcard, exercising dynamic-path
			// matching.
			if ca == "hls muxers" {
				cnf += "  mypath:\n"
			} else {
				cnf += "  all_others:\n"
			}

			p, ok := newInstance(cnf)
			require.Equal(t, true, ok)
			defer p.Close()

			tr := &http.Transport{}
			defer tr.CloseIdleConnections()
			hc := &http.Client{Transport: tr}

			medi := test.UniqueMediaH264()

			switch ca { //nolint:dupl
			case "rtsp conns", "rtsp sessions":
				source := gortsplib.Client{}

				err = source.StartRecording("rtsp://localhost:8554/mypath?key=val",
					&description.Session{Medias: []*description.Media{medi}})
				require.NoError(t, err)
				defer source.Close()

			case "rtsps conns", "rtsps sessions":
				source := gortsplib.Client{
					TLSConfig: &tls.Config{InsecureSkipVerify: true},
				}

				err = source.StartRecording("rtsps://localhost:8322/mypath?key=val",
					&description.Session{Medias: []*description.Media{medi}})
				require.NoError(t, err)
				defer source.Close()

			case "rtmp", "rtmps":
				var port string
				if ca == "rtmp" {
					port = "1935"
				} else {
					port = "1936"
				}

				var rawURL string

				if ca == "rtmps" {
					rawURL = "rtmps://"
				} else {
					rawURL = "rtmp://"
				}

				rawURL += "127.0.0.1:" + port + "/mypath?key=val"

				var u *url.URL
				u, err = url.Parse(rawURL)
				require.NoError(t, err)

				conn := &gortmplib.Client{
					URL:       u,
					TLSConfig: &tls.Config{InsecureSkipVerify: true},
					Publish:   true,
				}
				err = conn.Initialize(context.Background())
				require.NoError(t, err)
				defer conn.Close()

				track := &gortmplib.Track{
					Codec: &rtmpcodecs.H264{
						SPS: test.FormatH264.SPS,
						PPS: test.FormatH264.PPS,
					},
				}

				w := &gortmplib.Writer{
					Conn:   conn,
					Tracks: []*gortmplib.Track{track},
				}
				err = w.Initialize()
				require.NoError(t, err)

				err = w.WriteH264(track, 2*time.Second, 2*time.Second, [][]byte{{5, 2, 3, 4}})
				require.NoError(t, err)

				time.Sleep(500 * time.Millisecond)

			case "hls sessions", "hls muxers":
				source := gortsplib.Client{}
				err = source.StartRecording("rtsp://localhost:8554/mypath",
					&description.Session{Medias: []*description.Media{medi}})
				require.NoError(t, err)
				defer source.Close()

				go func() {
					time.Sleep(500 * time.Millisecond)

					for i := range 3 {
						/*source.WritePacketRTP(medi, &rtp.Packet{
							Header: rtp.Header{
								Version:        2,
								Marker:         true,
								PayloadType:    96,
								SequenceNumber: 123 + uint16(i),
								Timestamp:      45343 + uint32(i)*90000,
								SSRC:           563423,
							},
							Payload: []byte{
								testSPS,
								0x05,
							},
						})

						[]byte{ // 1920x1080 baseline
							0x67, 0x42, 0xc0, 0x28, 0xd9, 0x00, 0x78, 0x02,
							0x27, 0xe5, 0x84, 0x00, 0x00, 0x03, 0x00, 0x04,
							0x00, 0x00, 0x03, 0x00, 0xf0, 0x3c, 0x60, 0xc9, 0x20,
						},*/

						err2 := source.WritePacketRTP(medi, &rtp.Packet{
							Header: rtp.Header{
								Version:        2,
								Marker:         true,
								PayloadType:    96,
								SequenceNumber: 123 + uint16(i),
								Timestamp:      45343 + uint32(i)*90000,
								SSRC:           563423,
							},
							Payload: []byte{
								// testSPS,
								0x05,
							},
						})
						require.NoError(t, err2)
					}
				}()

				func() {
					res, err2 := hc.Get("http://localhost:8888/mypath/index.m3u8")
					require.NoError(t, err2)
					defer res.Body.Close()
					require.Equal(t, 200, res.StatusCode)
				}()

			case "webrtc":
				source := gortsplib.Client{}
				err = source.StartRecording("rtsp://localhost:8554/mypath",
					&description.Session{Medias: []*description.Media{medi}})
				require.NoError(t, err)
				defer source.Close()

				var u *url.URL
				u, err = url.Parse("http://localhost:8889/mypath/whep?key=val")
				require.NoError(t, err)

				go func() {
					time.Sleep(500 * time.Millisecond)

					err2 := source.WritePacketRTP(medi, &rtp.Packet{
						Header: rtp.Header{
							Version:        2,
							Marker:         true,
							PayloadType:    96,
							SequenceNumber: 123,
							Timestamp:      45343,
							SSRC:           563423,
						},
						Payload: []byte{5, 1, 2, 3, 4},
					})
					require.NoError(t, err2)
				}()

				c := &whip.Client{
					HTTPClient: hc,
					URL:        u,
					Log:        test.NilLogger,
				}

				err = c.Initialize(context.Background())
				require.NoError(t, err)
				defer checkClose(t, c.Close)

			case "srt":
				conf := srt.DefaultConfig()
				conf.StreamId = "publish:mypath:::key=val"

				var conn srt.Conn
				conn, err = srt.Dial("srt", "localhost:8890", conf)
				require.NoError(t, err)
				defer conn.Close()

				track := &mpegts.Track{
					Codec: &tscodecs.H264{},
				}

				bw := bufio.NewWriter(conn)
				w := &mpegts.Writer{W: bw, Tracks: []*mpegts.Track{track}}
				err = w.Initialize()
				require.NoError(t, err)

				err = w.WriteH264(track, 0, 0, [][]byte{{1}})
				require.NoError(t, err)

				err = bw.Flush()
				require.NoError(t, err)

				time.Sleep(500 * time.Millisecond)
			}

			// Cases that previously hit /v3/{rtsp,rtsps}{conns,sessions}/list,
			// /v3/{rtmp,rtmps,srt}conns/list, /v3/{webrtc,hls}sessions/list,
			// /v3/hlsmuxers/list now go through one of:
			//   * /v1/streams (with a protocol filter) for live sessions/conns,
			//   * /v1/recorder/hls-muxers (escape hatch) for HLS muxer state.
			//
			// Per ADR 0009 §D5 the previously-separate rtsp/rtsps connection
			// lists are folded into the parent rtsp(s) Stream's
			// protocol_specific.transport_connections[]. The "rtsp conns" and
			// "rtsps conns" cases assert on that nested array rather than on a
			// dedicated endpoint.
			cameraID := cameraIDFromPathName("mypath")

			if ca == "hls muxers" {
				// Escape-hatch list — pagination keys are snake_case per the
				// canonical /v1 convention; the per-muxer item shape retains
				// camelCase for muxer-specific fields per the escape-hatch
				// carve-out.
				var muxers struct {
					ItemCount int                `json:"item_count"`
					PageCount int                `json:"page_count"`
					Items     []defs.APIHLSMuxer `json:"items"`
				}
				httpRequest(t, hc, http.MethodGet,
					"http://localhost:9997/v1/recorder/hls-muxers", nil, &muxers)
				require.Equal(t, 1, muxers.ItemCount)
				require.Len(t, muxers.Items, 1)
				require.Equal(t, "mypath", muxers.Items[0].Path)

				// And the by-id form (now keyed by canonical Camera UUID).
				var oneMuxer defs.APIHLSMuxer
				httpRequest(t, hc, http.MethodGet,
					"http://localhost:9997/v1/recorder/hls-muxers/"+cameraID, nil, &oneMuxer)
				require.Equal(t, "mypath", oneMuxer.Path)
				require.Equal(t, muxers.Items[0].Created, oneMuxer.Created)
				return
			}

			// Map test-case → canonical Stream protocol value.
			var protocolFilter defs.StreamProtocol
			switch ca {
			case "rtsp conns", "rtsp sessions":
				protocolFilter = defs.StreamProtocolRTSP
			case "rtsps conns", "rtsps sessions":
				protocolFilter = defs.StreamProtocolRTSPS
			case "rtmp":
				protocolFilter = defs.StreamProtocolRTMP
			case "rtmps":
				protocolFilter = defs.StreamProtocolRTMPS
			case "hls sessions":
				protocolFilter = defs.StreamProtocolHLS
			case "webrtc":
				protocolFilter = defs.StreamProtocolWebRTC
			case "srt":
				protocolFilter = defs.StreamProtocolSRT
			}

			var streams struct {
				ItemCount int           `json:"item_count"`
				PageCount int           `json:"page_count"`
				Items     []defs.Stream `json:"items"`
			}
			httpRequest(t, hc, http.MethodGet,
				"http://localhost:9997/v1/streams?protocol="+string(protocolFilter),
				nil, &streams)
			require.GreaterOrEqual(t, streams.ItemCount, 1, "expected at least one %s stream", protocolFilter)

			// Find the stream for camera "mypath" (filter+camera_id is
			// equivalent; we look it up explicitly so the assertion message
			// is helpful when the stream is missing).
			var streamRef *defs.Stream
			for i := range streams.Items {
				if streams.Items[i].CameraID == cameraID {
					streamRef = &streams.Items[i]
					break
				}
			}
			require.NotNil(t, streamRef,
				"expected a %s stream for camera %s in /v1/streams response", protocolFilter, cameraID)
			require.Equal(t, protocolFilter, streamRef.Protocol)
			// Stream.id is a UUID (universal across protocols per ADR 0009).
			require.NotEmpty(t, streamRef.ID)
			// Remote address is PII and gets redacted at the API boundary.
			require.Equal(t, "redacted", streamRef.RemoteAddr)

			// Direction depends on the protocol — webrtc is the WHEP read
			// session in this fixture; hls is read-by-definition; everything
			// else is publish.
			expectedDirection := defs.StreamDirectionPublish
			if ca == "webrtc" || ca == "hls sessions" {
				expectedDirection = defs.StreamDirectionRead
			}
			require.Equal(t, expectedDirection, streamRef.Direction)

			// Protocol-specific assertions: cases that previously used the
			// per-protocol-conns endpoints now read the nested
			// transport_connections array on the parent Stream. Stream's
			// custom UnmarshalJSON dispatches ProtocolSpecific by Protocol.
			if ca == "rtsp conns" || ca == "rtsps conns" {
				ps, ok := streamRef.ProtocolSpecific.(*defs.ProtocolSpecificRTSP)
				require.True(t, ok, "expected ProtocolSpecificRTSP on rtsp(s) stream")
				require.NotEmpty(t, ps.TransportConnections,
					"expected at least one transport_connection on the parent rtsp(s) stream")
				// Per the redaction policy, the transport_connections array
				// also has remote_addr redacted at the API edge.
				require.Equal(t, "redacted", ps.TransportConnections[0].RemoteAddr)
			}

			// Get-by-id round-trip: GET /v1/streams/{id} returns the same id.
			var oneStream defs.Stream
			httpRequest(t, hc, http.MethodGet,
				"http://localhost:9997/v1/streams/"+streamRef.ID, nil, &oneStream)
			require.Equal(t, streamRef.ID, oneStream.ID)
			require.Equal(t, streamRef.Protocol, oneStream.Protocol)
			require.Equal(t, streamRef.CameraID, oneStream.CameraID)
		})
	}
}

// TestAPIProtocolGetNotFound asserts the canonical /v1 surface returns 404
// for unknown stream / muxer IDs. The legacy /v3 surface had per-protocol
// "connection not found" / "session not found" / "muxer not found" error
// messages; the unified /v1/streams handler collapses the first two into
// "stream not found", and /v1/recorder/hls-muxers/{id} surfaces
// "camera not found" when the UUID does not correspond to a configured
// camera (per ADR 0009 §D5/§D6).
func TestAPIProtocolGetNotFound(t *testing.T) {
	serverCertFpath, err := test.CreateTempFile(test.TLSCertPub)
	require.NoError(t, err)
	defer os.Remove(serverCertFpath)

	serverKeyFpath, err := test.CreateTempFile(test.TLSCertKey)
	require.NoError(t, err)
	defer os.Remove(serverKeyFpath)

	// We need two distinct cases now:
	//   * stream-shaped (everything that used to be a protocol session/conn)
	//     → /v1/streams/{id} returns "stream not found".
	//   * hls-muxers escape hatch → /v1/recorder/hls-muxers/{id} returns
	//     "camera not found" when the id does not map to any configured camera.
	for _, ca := range []string{
		"streams",
		"hls muxers",
	} {
		t.Run(ca, func(t *testing.T) {
			cnf := "api: yes\n" +
				"rtspEncryption: strict\n" +
				"rtspTransports: [tcp]\n" +
				"rtspServerCert: " + serverCertFpath + "\n" +
				"rtspServerKey: " + serverKeyFpath + "\n" +
				"rtmpEncryption: strict\n" +
				"rtmpServerCert: " + serverCertFpath + "\n" +
				"rtmpServerKey: " + serverKeyFpath + "\n" +
				"paths:\n" +
				"  all_others:\n"

			p, ok := newInstance(cnf)
			require.Equal(t, true, ok)
			defer p.Close()

			tr := &http.Transport{}
			defer tr.CloseIdleConnections()
			hc := &http.Client{Transport: tr}

			var url, expectedErr string
			switch ca {
			case "streams":
				url = "http://localhost:9997/v1/streams/" + uuid.New().String()
				expectedErr = "stream not found"
			case "hls muxers":
				url = "http://localhost:9997/v1/recorder/hls-muxers/" + uuid.New().String()
				expectedErr = "camera not found"
			}

			req, err2 := http.NewRequest(http.MethodGet, url, nil)
			require.NoError(t, err2)

			res, err2 := hc.Do(req)
			require.NoError(t, err2)
			defer res.Body.Close()

			require.Equal(t, http.StatusNotFound, res.StatusCode)
			checkError(t, expectedErr, res.Body)
		})
	}
}

func TestAPIProtocolKick(t *testing.T) {
	serverCertFpath, err := test.CreateTempFile(test.TLSCertPub)
	require.NoError(t, err)
	defer os.Remove(serverCertFpath)

	serverKeyFpath, err := test.CreateTempFile(test.TLSCertKey)
	require.NoError(t, err)
	defer os.Remove(serverKeyFpath)

	for _, ca := range []string{
		"rtsp",
		"rtsps",
		"rtmp",
		"hls",
		"webrtc",
		"srt",
	} {
		t.Run(ca, func(t *testing.T) {
			cnf := "api: yes\n"

			if ca == "rtsps" {
				cnf += "rtspTransports: [tcp]\n" +
					"rtspEncryption: strict\n" +
					"rtspServerCert: " + serverCertFpath + "\n" +
					"rtspServerKey: " + serverKeyFpath + "\n"
			}

			cnf += "paths:\n" +
				"  all_others:\n"

			p, ok := newInstance(cnf)
			require.Equal(t, true, ok)
			defer p.Close()

			tr := &http.Transport{}
			defer tr.CloseIdleConnections()
			hc := &http.Client{Transport: tr}

			medi := test.MediaH264

			switch ca {
			case "rtsp":
				source := gortsplib.Client{}
				err = source.StartRecording("rtsp://localhost:8554/mypath",
					&description.Session{Medias: []*description.Media{medi}})
				require.NoError(t, err)
				defer source.Close()

			case "rtsps":
				source := gortsplib.Client{
					TLSConfig: &tls.Config{InsecureSkipVerify: true},
				}
				err = source.StartRecording("rtsps://localhost:8322/mypath",
					&description.Session{Medias: []*description.Media{medi}})
				require.NoError(t, err)
				defer source.Close()

			case "rtmp":
				var u *url.URL
				u, err = url.Parse("rtmp://localhost:1935/mypath")
				require.NoError(t, err)

				conn := &gortmplib.Client{
					URL:     u,
					Publish: true,
				}
				err = conn.Initialize(context.Background())
				require.NoError(t, err)
				defer conn.Close()

				track := &gortmplib.Track{
					Codec: &rtmpcodecs.H264{
						SPS: test.FormatH264.SPS,
						PPS: test.FormatH264.PPS,
					},
				}

				w := &gortmplib.Writer{
					Conn:   conn,
					Tracks: []*gortmplib.Track{track},
				}
				err = w.Initialize()
				require.NoError(t, err)

				err = w.WriteH264(track, 2*time.Second, 2*time.Second, [][]byte{{5, 2, 3, 4}})
				require.NoError(t, err)

			case "hls":
				source := gortsplib.Client{}
				err = source.StartRecording("rtsp://localhost:8554/mypath",
					&description.Session{Medias: []*description.Media{medi}})
				require.NoError(t, err)
				defer source.Close()

				var enc *rtph264.Encoder
				enc, err = medi.Formats[0].(*format.H264).CreateEncoder()
				require.NoError(t, err)

				tracksReceived := make(chan struct{})

				client := &gohlslib.Client{
					URI: "http://localhost:8888/mypath/index.m3u8",
					OnTracks: func(_ []*gohlslib.Track) error {
						close(tracksReceived)
						return nil
					},
				}
				err = client.Start()
				require.NoError(t, err)
				defer client.Close()

				time.Sleep(500 * time.Millisecond)

				for i := range 2 {
					var pkts []*rtp.Packet
					pkts, err = enc.Encode([][]byte{{5, 2, 3, 4}})
					require.NoError(t, err)

					pkts[0].Timestamp = uint32(i * 90000)

					err = source.WritePacketRTP(medi, pkts[0])
					require.NoError(t, err)
				}

				<-tracksReceived

			case "webrtc":
				var u *url.URL
				u, err = url.Parse("http://localhost:8889/mypath/whip")
				require.NoError(t, err)

				track := &webrtc.OutgoingTrack{
					Caps: pwebrtc.RTPCodecCapability{
						MimeType:    pwebrtc.MimeTypeH264,
						ClockRate:   90000,
						SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
					},
				}

				c := &whip.Client{
					HTTPClient:     hc,
					URL:            u,
					Log:            test.NilLogger,
					Publish:        true,
					OutgoingTracks: []*webrtc.OutgoingTrack{track},
				}

				err = c.Initialize(context.Background())
				require.NoError(t, err)
				defer func() {
					require.Error(t, c.Close())
				}()

			case "srt":
				conf := srt.DefaultConfig()
				conf.StreamId = "publish:mypath"

				var conn srt.Conn
				conn, err = srt.Dial("srt", "localhost:8890", conf)
				require.NoError(t, err)
				defer conn.Close()

				track := &mpegts.Track{
					Codec: &tscodecs.H264{},
				}

				bw := bufio.NewWriter(conn)
				w := &mpegts.Writer{W: bw, Tracks: []*mpegts.Track{track}}
				err = w.Initialize()
				require.NoError(t, err)

				err = w.WriteH264(track, 0, 0, [][]byte{{1}})
				require.NoError(t, err)

				err = bw.Flush()
				require.NoError(t, err)
			}

			// Per ADR 0009 §D5 the per-protocol kick endpoints
			// (POST /v3/{rtspsessions,rtmpconns,…}/kick/{id}) collapse into
			// DELETE /v1/streams/{id}. Filter the unified list by protocol so
			// we kick only the stream this case set up.
			var protocolFilter defs.StreamProtocol
			switch ca {
			case "rtsp":
				protocolFilter = defs.StreamProtocolRTSP
			case "rtsps":
				protocolFilter = defs.StreamProtocolRTSPS
			case "rtmp":
				protocolFilter = defs.StreamProtocolRTMP
			case "hls":
				protocolFilter = defs.StreamProtocolHLS
			case "webrtc":
				protocolFilter = defs.StreamProtocolWebRTC
			case "srt":
				protocolFilter = defs.StreamProtocolSRT
			}

			var out1 struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
			}
			httpRequest(t, hc, http.MethodGet,
				"http://localhost:9997/v1/streams?protocol="+string(protocolFilter), nil, &out1)
			require.NotEmpty(t, out1.Items)

			httpRequest(t, hc, http.MethodDelete,
				"http://localhost:9997/v1/streams/"+out1.Items[0].ID, nil, nil)

			var out2 struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
			}
			httpRequest(t, hc, http.MethodGet,
				"http://localhost:9997/v1/streams?protocol="+string(protocolFilter), nil, &out2)
			require.Empty(t, out2.Items)
		})
	}
}

// TestAPIProtocolKickNotFound asserts the unified DELETE /v1/streams/{id}
// returns 404 with the canonical "stream not found" error envelope when
// the id does not match any active session/connection. The legacy
// /v3/{rtspsessions,rtmpconns,…}/kick/{id} endpoints had per-protocol
// "connection not found" / "session not found" error messages; ADR 0009
// §D5 collapses both into a single canonical error.
func TestAPIProtocolKickNotFound(t *testing.T) {
	serverCertFpath, err := test.CreateTempFile(test.TLSCertPub)
	require.NoError(t, err)
	defer os.Remove(serverCertFpath)

	serverKeyFpath, err := test.CreateTempFile(test.TLSCertKey)
	require.NoError(t, err)
	defer os.Remove(serverKeyFpath)

	cnf := "api: yes\n" +
		"rtspTransports: [tcp]\n" +
		"rtspEncryption: strict\n" +
		"rtspServerCert: " + serverCertFpath + "\n" +
		"rtspServerKey: " + serverKeyFpath + "\n" +
		"paths:\n" +
		"  all_others:\n"

	p, ok := newInstance(cnf)
	require.Equal(t, true, ok)
	defer p.Close()

	tr := &http.Transport{}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr}

	req, err := http.NewRequest(http.MethodDelete,
		"http://localhost:9997/v1/streams/"+uuid.New().String(), nil)
	require.NoError(t, err)

	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()

	require.Equal(t, http.StatusNotFound, res.StatusCode)
	checkError(t, "stream not found", res.Body)
}
