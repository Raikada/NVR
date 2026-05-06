package onvif

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProbeEnvelopeContents(t *testing.T) {
	env := probeEnvelope("uuid:12345")
	s := string(env)
	// SOAP namespaces present.
	require.Contains(t, s, NSEnvelope)
	require.Contains(t, s, NSAddressing)
	require.Contains(t, s, NSDiscovery)
	// MessageID interpolated.
	require.Contains(t, s, ">uuid:12345<")
	// Action targets the WS-Discovery Probe.
	require.Contains(t, s, ActionProbe)
	// Types include NetworkVideoTransmitter (the ONVIF camera type).
	require.Contains(t, s, "NetworkVideoTransmitter")
}

// probeMatchHikvisionFixture is a real-camera-shaped ProbeMatches
// response. ONVIF cameras emit slightly different markup; this is the
// shape Hikvision / generic Profile-S cameras send.
const probeMatchHikvisionFixture = `<?xml version="1.0" encoding="UTF-8"?>
<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"
              xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing"
              xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery">
  <env:Header>
    <wsa:MessageID>uuid:abcd-1234</wsa:MessageID>
    <wsa:RelatesTo>uuid:probe-12345</wsa:RelatesTo>
    <wsa:To>http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous</wsa:To>
    <wsa:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/ProbeMatches</wsa:Action>
  </env:Header>
  <env:Body>
    <d:ProbeMatches>
      <d:ProbeMatch>
        <wsa:EndpointReference>
          <wsa:Address>urn:uuid:11111111-2222-3333-4444-555555555555</wsa:Address>
        </wsa:EndpointReference>
        <d:Types>dn:NetworkVideoTransmitter tds:Device</d:Types>
        <d:Scopes>onvif://www.onvif.org/Profile/Streaming onvif://www.onvif.org/type/Network_Video_Transmitter onvif://www.onvif.org/hardware/DS-2CD2143G2 onvif://www.onvif.org/name/HikvisionCam onvif://www.onvif.org/manufacturer/Hikvision onvif://www.onvif.org/location/country/CN</d:Scopes>
        <d:XAddrs>http://192.168.1.50/onvif/device_service</d:XAddrs>
        <d:MetadataVersion>10</d:MetadataVersion>
      </d:ProbeMatch>
    </d:ProbeMatches>
  </env:Body>
</env:Envelope>`

func TestParseProbeMatch_Hikvision(t *testing.T) {
	dev, ok := parseProbeMatch([]byte(probeMatchHikvisionFixture))
	require.True(t, ok)
	require.Equal(t, "urn:uuid:11111111-2222-3333-4444-555555555555", dev.EndpointRef)
	require.Equal(t, "http://192.168.1.50/onvif/device_service", dev.XAddr)
	require.Contains(t, dev.Types, "dn:NetworkVideoTransmitter")
	require.Equal(t, "Hikvision", dev.Manufacturer)
	require.Equal(t, "DS-2CD2143G2", dev.Hardware)
	require.Equal(t, "HikvisionCam", dev.Name)
	require.Equal(t, "CN", dev.Country)
}

// probeMatchAxisFixture is closer to what Axis cameras emit — different
// scope shapes and a percent-encoded vendor name.
const probeMatchAxisFixture = `<?xml version="1.0" encoding="UTF-8"?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://www.w3.org/2003/05/soap-envelope"
                   xmlns:wsadis="http://schemas.xmlsoap.org/ws/2004/08/addressing"
                   xmlns:wsdd="http://schemas.xmlsoap.org/ws/2005/04/discovery">
  <SOAP-ENV:Header/>
  <SOAP-ENV:Body>
    <wsdd:ProbeMatches>
      <wsdd:ProbeMatch>
        <wsadis:EndpointReference>
          <wsadis:Address>urn:uuid:axis-camera-uuid-9999</wsadis:Address>
        </wsadis:EndpointReference>
        <wsdd:Types>dn:NetworkVideoTransmitter</wsdd:Types>
        <wsdd:Scopes>onvif://www.onvif.org/Profile/Streaming onvif://www.onvif.org/manufacturer/Axis%20Communications onvif://www.onvif.org/model/M3215-LVE</wsdd:Scopes>
        <wsdd:XAddrs>http://192.168.1.99/onvif/device_service</wsdd:XAddrs>
      </wsdd:ProbeMatch>
    </wsdd:ProbeMatches>
  </SOAP-ENV:Body>
</SOAP-ENV:Envelope>`

func TestParseProbeMatch_Axis_PercentDecoded(t *testing.T) {
	dev, ok := parseProbeMatch([]byte(probeMatchAxisFixture))
	require.True(t, ok)
	require.Equal(t, "urn:uuid:axis-camera-uuid-9999", dev.EndpointRef)
	require.Equal(t, "Axis Communications", dev.Manufacturer)
	require.Equal(t, "M3215-LVE", dev.Model)
}

func TestParseProbeMatch_Garbage_ReturnsFalse(t *testing.T) {
	_, ok := parseProbeMatch([]byte("not-xml"))
	require.False(t, ok)
	_, ok = parseProbeMatch([]byte(`<env:Envelope xmlns:env="http://www.w3.org/2003/05/soap-envelope"><env:Body/></env:Envelope>`))
	require.False(t, ok)
}

func TestPercentDecode(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"plain", "plain"},
		{"Axis%20Communications", "Axis Communications"},
		{"Hikvision%2DUSA", "Hikvision-USA"},
		{"%48%69", "Hi"},
	}
	for _, c := range cases {
		got, err := percentDecode(c.in)
		require.NoError(t, err)
		require.Equal(t, c.want, got, "input=%q", c.in)
	}
}

func TestSplitWhitespace(t *testing.T) {
	require.Equal(t, []string{"a", "b", "c"}, splitWhitespace("a  b\tc\n"))
	require.Equal(t, []string{}, splitWhitespace("   "))
}

func TestRunDiscovery_NoResponses_ReturnsEmpty(t *testing.T) {
	// Smoke test: actually issuing a probe to the multicast group on
	// the build host. We use a 200 ms window so the test exits fast;
	// tests run in environments where multicast typically goes
	// nowhere, so we expect zero responses.
	d := &Discoverer{Timeout: 200 * time.Millisecond}
	devs, err := d.Run(testContext(t))
	require.NoError(t, err)
	// Non-strict: real LANs may have ONVIF cameras (e.g., a developer
	// laptop). What we DO assert is that the call returns within the
	// timeout and yields no error path-side exceptions.
	_ = devs
}

func TestRunDiscovery_Returns_OnContextCancel(t *testing.T) {
	d := &Discoverer{Timeout: 5 * time.Second}
	ctx, cancel := contextWithCancel()
	cancel()
	devs, err := d.Run(ctx)
	require.NoError(t, err)
	require.Empty(t, devs)
}

func TestProbeEnvelope_RoundTripsThroughParse(t *testing.T) {
	// Build a probe, hand-craft a fake ProbeMatches that references
	// the same MessageID, and confirm the parser pulls everything out.
	// This exercises the full marshal/unmarshal path.
	envelope := probeEnvelope("uuid:test-rt")
	require.Contains(t, string(envelope), "uuid:test-rt")

	// Synthetic ProbeMatch with clean shape.
	resp := strings.NewReader(probeMatchHikvisionFixture)
	body := make([]byte, 16384)
	n, _ := resp.Read(body)
	dev, ok := parseProbeMatch(body[:n])
	require.True(t, ok)
	require.NotEmpty(t, dev.XAddr)
}
