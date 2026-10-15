package onvif

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Fixtures below are trimmed captures of Amcrest IP5M-T1277EW-AI SOAP
// responses (namespace prefixes as the camera emits them).

const fixtureGetCapabilities = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tds="http://www.onvif.org/ver10/device/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema">
<s:Body>
<tds:GetCapabilitiesResponse>
<tds:Capabilities>
<tt:Device><tt:XAddr>http://192.168.1.110/onvif/device_service</tt:XAddr>
<tt:IO><tt:InputConnectors>1</tt:InputConnectors><tt:RelayOutputs>1</tt:RelayOutputs></tt:IO>
</tt:Device>
<tt:Events><tt:XAddr>http://192.168.1.110/onvif/event_service</tt:XAddr>
<tt:WSPullPointSupport>true</tt:WSPullPointSupport></tt:Events>
<tt:Imaging><tt:XAddr>http://192.168.1.110/onvif/imaging_service</tt:XAddr></tt:Imaging>
<tt:Media><tt:XAddr>http://192.168.1.110/onvif/media_service</tt:XAddr></tt:Media>
<tt:PTZ><tt:XAddr>http://192.168.1.110/onvif/ptz_service</tt:XAddr></tt:PTZ>
</tds:Capabilities>
</tds:GetCapabilitiesResponse>
</s:Body>
</s:Envelope>`

const fixtureGetProfiles = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:trt="http://www.onvif.org/ver10/media/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema">
<s:Body>
<trt:GetProfilesResponse>
<trt:Profiles token="MediaProfile000" fixed="true">
<tt:Name>MainStream</tt:Name>
<tt:VideoEncoderConfiguration token="VideoEncoderConfig000">
<tt:Name>VideoEncoder_1</tt:Name>
<tt:Encoding>H264</tt:Encoding>
<tt:Resolution><tt:Width>2960</tt:Width><tt:Height>1668</tt:Height></tt:Resolution>
</tt:VideoEncoderConfiguration>
<tt:AudioEncoderConfiguration token="AudioEncoderConfig000">
<tt:Name>AudioEncoder_1</tt:Name>
<tt:Encoding>AAC</tt:Encoding>
</tt:AudioEncoderConfiguration>
</trt:Profiles>
<trt:Profiles token="MediaProfile001" fixed="true">
<tt:Name>SubStream</tt:Name>
<tt:VideoEncoderConfiguration token="VideoEncoderConfig001">
<tt:Name>VideoEncoder_2</tt:Name>
<tt:Encoding>H264</tt:Encoding>
<tt:Resolution><tt:Width>640</tt:Width><tt:Height>480</tt:Height></tt:Resolution>
</tt:VideoEncoderConfiguration>
</trt:Profiles>
</trt:GetProfilesResponse>
</s:Body>
</s:Envelope>`

const fixtureGetStreamURI = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:trt="http://www.onvif.org/ver10/media/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema">
<s:Body>
<trt:GetStreamUriResponse>
<trt:MediaUri>
<tt:Uri>rtsp://user:secret@192.168.1.110:554/cam/realmonitor?channel=1&amp;subtype=0&amp;unicast=true&amp;proto=Onvif</tt:Uri>
<tt:InvalidAfterConnect>false</tt:InvalidAfterConnect>
</trt:MediaUri>
</trt:GetStreamUriResponse>
</s:Body>
</s:Envelope>`

const fixtureGetSnapshotURI = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:trt="http://www.onvif.org/ver10/media/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema">
<s:Body>
<trt:GetSnapshotUriResponse>
<trt:MediaUri>
<tt:Uri>http://192.168.1.110/onvifsnapshot/media_service/snapshot?channel=1&amp;subtype=0</tt:Uri>
</trt:MediaUri>
</trt:GetSnapshotUriResponse>
</s:Body>
</s:Envelope>`

// fixtureServer answers each SOAP action with its fixture and records
// the request body for assertion.
func fixtureServer(t *testing.T, responses map[string]string) (*httptest.Server, *[]string) {
	t.Helper()
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1<<20)
		n, _ := r.Body.Read(buf)
		body := string(buf[:n])
		bodies = append(bodies, body)
		for marker, resp := range responses {
			if strings.Contains(body, marker) {
				w.Header().Set("Content-Type", "application/soap+xml")
				_, _ = w.Write([]byte(resp))
				return
			}
		}
		http.Error(w, "no fixture for request", http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

func TestGetCapabilitiesParsesAmcrest(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{"GetCapabilities": fixtureGetCapabilities})

	dc := &DeviceClient{XAddr: srv.URL, Username: "admin", Password: "pw"}
	caps, err := dc.GetCapabilities(context.Background())
	require.NoError(t, err)
	require.Equal(t, "http://192.168.1.110/onvif/media_service", caps.MediaXAddr)
	require.Equal(t, "http://192.168.1.110/onvif/event_service", caps.EventsXAddr)
	require.True(t, caps.HasPTZ)
	require.True(t, caps.HasImaging)
	require.True(t, caps.HasIO)
}

func TestGetProfilesParsesAmcrest(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{"GetProfiles": fixtureGetProfiles})

	mc := &MediaClient{XAddr: srv.URL, Username: "admin", Password: "pw"}
	profiles, err := mc.GetProfiles(context.Background())
	require.NoError(t, err)
	require.Len(t, profiles, 2)

	require.Equal(t, "MediaProfile000", profiles[0].Token)
	require.Equal(t, "MainStream", profiles[0].Name)
	require.Equal(t, "H264", profiles[0].VideoCodec)
	require.Equal(t, 2960, profiles[0].Width)
	require.Equal(t, 1668, profiles[0].Height)
	require.True(t, profiles[0].HasAudio)

	require.Equal(t, "MediaProfile001", profiles[1].Token)
	require.False(t, profiles[1].HasAudio)
}

func TestGetStreamURIStripsUserinfo(t *testing.T) {
	srv, bodies := fixtureServer(t, map[string]string{"GetStreamUri": fixtureGetStreamURI})

	mc := &MediaClient{XAddr: srv.URL, Username: "admin", Password: "pw"}
	uri, err := mc.GetStreamURI(context.Background(), "MediaProfile000")
	require.NoError(t, err)
	// Userinfo the camera embeds must be stripped: credentials live in
	// the vault, never in stored source URLs.
	require.Equal(t,
		"rtsp://192.168.1.110:554/cam/realmonitor?channel=1&subtype=0&unicast=true&proto=Onvif", uri)
	// The requested profile token must ride in the request body.
	require.Contains(t, (*bodies)[0], "MediaProfile000")
}

func TestGetSnapshotURI(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{"GetSnapshotUri": fixtureGetSnapshotURI})

	mc := &MediaClient{XAddr: srv.URL, Username: "admin", Password: "pw"}
	uri, err := mc.GetSnapshotURI(context.Background(), "MediaProfile000")
	require.NoError(t, err)
	require.Equal(t, "http://192.168.1.110/onvifsnapshot/media_service/snapshot?channel=1&subtype=0", uri)
}

func TestMediaClientAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "auth", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	mc := &MediaClient{XAddr: srv.URL, Username: "admin", Password: "wrong"}
	_, err := mc.GetProfiles(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "authentication")
}
