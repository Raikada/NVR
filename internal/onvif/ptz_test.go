package onvif

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

const fixturePTZOK = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl">
<s:Body><tptz:ContinuousMoveResponse/></s:Body>
</s:Envelope>`

const fixtureGetPresets = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema">
<s:Body>
<tptz:GetPresetsResponse>
<tptz:Preset token="Preset001"><tt:Name>Driveway</tt:Name></tptz:Preset>
<tptz:Preset token="Preset002"><tt:Name>Gate</tt:Name></tptz:Preset>
</tptz:GetPresetsResponse>
</s:Body>
</s:Envelope>`

const fixtureSetPreset = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl">
<s:Body><tptz:SetPresetResponse><tptz:PresetToken>Preset003</tptz:PresetToken></tptz:SetPresetResponse></s:Body>
</s:Envelope>`

func TestPTZContinuousMoveAndStop(t *testing.T) {
	srv, bodies := fixtureServer(t, map[string]string{
		"ContinuousMove": fixturePTZOK,
		"Stop":           fixturePTZOK,
	})
	pc := &PTZClient{XAddr: srv.URL, Username: "admin", Password: "pw"}

	require.NoError(t, pc.ContinuousMove(context.Background(), "Profile000", 0.5, -0.25, 0.1))
	require.NoError(t, pc.Stop(context.Background(), "Profile000"))

	require.Contains(t, (*bodies)[0], "Profile000")
	require.Contains(t, (*bodies)[0], "0.5")
	require.Contains(t, (*bodies)[0], "-0.25")
	require.Contains(t, (*bodies)[1], "Stop")
}

func TestPTZGotoHome(t *testing.T) {
	srv, _ := fixtureServer(t, map[string]string{"GotoHomePosition": fixturePTZOK})
	pc := &PTZClient{XAddr: srv.URL, Username: "admin", Password: "pw"}
	require.NoError(t, pc.GotoHome(context.Background(), "Profile000"))
}

func TestPTZPresets(t *testing.T) {
	srv, bodies := fixtureServer(t, map[string]string{
		"GetPresets": fixtureGetPresets,
		"SetPreset":  fixtureSetPreset,
		"GotoPreset": fixturePTZOK,
	})
	pc := &PTZClient{XAddr: srv.URL, Username: "admin", Password: "pw"}

	presets, err := pc.GetPresets(context.Background(), "Profile000")
	require.NoError(t, err)
	require.Len(t, presets, 2)
	require.Equal(t, "Preset001", presets[0].Token)
	require.Equal(t, "Driveway", presets[0].Name)

	tok, err := pc.SetPreset(context.Background(), "Profile000", "Porch")
	require.NoError(t, err)
	require.Equal(t, "Preset003", tok)
	require.Contains(t, (*bodies)[1], "Porch")

	require.NoError(t, pc.GotoPreset(context.Background(), "Profile000", "Preset002"))
	require.Contains(t, (*bodies)[2], "Preset002")
}

func TestDeviceSystemReboot(t *testing.T) {
	const fixtureReboot = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tds="http://www.onvif.org/ver10/device/wsdl">
<s:Body><tds:SystemRebootResponse><tds:Message>Rebooting in 30 seconds</tds:Message></tds:SystemRebootResponse></s:Body>
</s:Envelope>`
	srv, _ := fixtureServer(t, map[string]string{"SystemReboot": fixtureReboot})
	dc := &DeviceClient{XAddr: srv.URL, Username: "admin", Password: "pw"}
	msg, err := dc.SystemReboot(context.Background())
	require.NoError(t, err)
	require.Contains(t, msg, "Rebooting")
}

func TestPTZAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "denied", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	pc := &PTZClient{XAddr: srv.URL, Username: "admin", Password: "wrong"}
	err := pc.Stop(context.Background(), "p")
	require.Error(t, err)
}
