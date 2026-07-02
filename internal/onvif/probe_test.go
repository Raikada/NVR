package onvif

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

const fixtureDeviceInfoAmcrest = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tds="http://www.onvif.org/ver10/device/wsdl">
<s:Body>
<tds:GetDeviceInformationResponse>
<tds:Manufacturer>Amcrest</tds:Manufacturer>
<tds:Model>IP5M-T1277EW-AI</tds:Model>
<tds:FirmwareVersion>V2.800.00AC003.0.R</tds:FirmwareVersion>
<tds:SerialNumber>AMC094B09882D5F152</tds:SerialNumber>
<tds:HardwareId>1.00</tds:HardwareId>
</tds:GetDeviceInformationResponse>
</s:Body>
</s:Envelope>`

func TestSelectProfilePrefersResolutionThenAudio(t *testing.T) {
	cases := []struct {
		name     string
		profiles []MediaProfile
		want     string
	}{
		{
			name: "highest resolution wins",
			profiles: []MediaProfile{
				{Token: "sub", Width: 640, Height: 480},
				{Token: "main", Width: 2960, Height: 1668},
			},
			want: "main",
		},
		{
			name: "audio breaks resolution tie",
			profiles: []MediaProfile{
				{Token: "silent", Width: 1920, Height: 1080},
				{Token: "with_audio", Width: 1920, Height: 1080, HasAudio: true},
			},
			want: "with_audio",
		},
		{
			name:     "single profile",
			profiles: []MediaProfile{{Token: "only", Width: 0, Height: 0}},
			want:     "only",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := selectProfile(tc.profiles)
			require.Equal(t, tc.want, got.Token)
		})
	}
}

func TestSelectProfileEmpty(t *testing.T) {
	require.Nil(t, selectProfile(nil))
}

// End-to-end probe against a fixture server that speaks every SOAP
// action the probe issues. The media XAddr in the GetCapabilities
// fixture points at 192.168.1.110; the probe must rewrite it onto the
// test server host (cameras behind NAT/rebinding report their LAN
// address — we always talk to the host we probed).
func TestProbeCapabilitiesAmcrest(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{
		"GetDeviceInformation": fixtureDeviceInfoAmcrest,
		"GetCapabilities":      fixtureGetCapabilities,
		"GetProfiles":          fixtureGetProfiles,
		"GetStreamUri":         fixtureGetStreamURI,
		"GetSnapshotUri":       fixtureGetSnapshotURI,
	})

	report, err := ProbeCapabilities(context.Background(), srv.URL, "admin", "pw")
	require.NoError(t, err)

	require.Equal(t, "Amcrest", report.Device.Manufacturer)
	require.Equal(t, "IP5M-T1277EW-AI", report.Device.Model)
	require.Len(t, report.Profiles, 2)
	require.Equal(t, "MediaProfile000", report.SelectedToken)
	require.Equal(t,
		"rtsp://192.168.1.110:554/cam/realmonitor?channel=1&subtype=0&unicast=true&proto=Onvif",
		report.StreamURI)
	require.Equal(t,
		"http://192.168.1.110/onvifsnapshot/media_service/snapshot?channel=1&subtype=0",
		report.SnapshotURI)
	require.True(t, report.HasAudio)
	require.True(t, report.HasPTZ)
	require.True(t, report.HasMotion) // events XAddr present
	require.True(t, report.HasIO)
	require.True(t, report.HasImaging)
}

func TestProbeCapabilitiesBadCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "denied", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	_, err := ProbeCapabilities(context.Background(), srv.URL, "admin", "wrong")
	require.ErrorIs(t, err, ErrUnauthorized)
}
