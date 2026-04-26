package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactSourceURL(t *testing.T) {
	for _, ca := range []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"no scheme", "host:1935/stream", "host:1935/stream"},
		{"no userinfo", "rtsp://host:554/stream", "rtsp://host:554/stream"},
		{"with userinfo", "rtsp://alice:s3cret@cam.local/stream", "rtsp://redacted:redacted@cam.local/stream"},
		{"userinfo with port", "rtsp://bob:hunter2@10.0.0.1:554/cam1", "rtsp://redacted:redacted@10.0.0.1:554/cam1"},
		{"udp+rtp variant scheme", "udp+rtp://eve:p@ss@239.0.0.1:9004", "udp+rtp://redacted:redacted@239.0.0.1:9004"},
		{"unparseable url", "not a url with @", "not a url with @"},
	} {
		t.Run(ca.name, func(t *testing.T) {
			require.Equal(t, ca.want, redactSourceURL(ca.in))
		})
	}
}
