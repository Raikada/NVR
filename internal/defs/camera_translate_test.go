package defs

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSourceTypeFromConfSource_MpegTSUDP regression-locks the OQ6 → D9
// resolution: udp://, udp+mpegts://, and unix+mpegts:// source URLs
// resolve to CameraSourceTypeMpegTSUDP, not the prior Phase-1B
// CameraSourceTypeFile fallback.
func TestSourceTypeFromConfSource_MpegTSUDP(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"plain_udp", "udp://multicast.iptv.example.com:5000"},
		{"udp_mpegts", "udp+mpegts://multicast.iptv.example.com:5000"},
		{"unix_mpegts", "unix+mpegts:///var/run/mediamtx/in.sock"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, CameraSourceTypeMpegTSUDP, sourceTypeFromConfSource(c.src),
				"source URL %q should map to source_type=mpegts_udp per ADR 0009 §D9", c.src)
		})
	}
}

// TestSourceTypeFromConfSource_RTPSDPNotMpegTS guards against
// regression: udp+rtp:// and unix+rtp:// must continue to resolve
// to RTP (which uses an SDP descriptor file), not mpegts_udp.
func TestSourceTypeFromConfSource_RTPSDPNotMpegTS(t *testing.T) {
	require.Equal(t, CameraSourceTypeRTP, sourceTypeFromConfSource("udp+rtp://192.0.2.1:5000"))
	require.Equal(t, CameraSourceTypeRTP, sourceTypeFromConfSource("unix+rtp:///var/run/foo.sock"))
}
