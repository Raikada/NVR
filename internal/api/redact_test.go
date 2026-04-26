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

func TestRedactQueryString(t *testing.T) {
	for _, ca := range []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"non-sensitive (passthrough)", "foo=bar&baz=qux", "foo=bar&baz=qux"},
		{"token", "token=abc123", "token=redacted"},
		{"password", "password=hunter2", "password=redacted"},
		{"key", "key=secret_value", "key=redacted"},
		{"secret", "secret=open_sesame", "secret=redacted"},
		{
			"all four together",
			"token=t1&password=p1&key=k1&secret=s1",
			"key=redacted&password=redacted&secret=redacted&token=redacted",
		},
		{
			"mixed sensitive and non-sensitive",
			"foo=bar&token=xyz&baz=qux",
			"baz=qux&foo=bar&token=redacted",
		},
		{
			"case insensitive on key",
			"Token=abc&PASSWORD=def",
			"PASSWORD=redacted&Token=redacted",
		},
		{
			"substring match (access_token, apikey)",
			"access_token=abc&apikey=def",
			"access_token=redacted&apikey=redacted",
		},
		{
			"value with special chars survives encoding",
			"normal=hello%20world&token=abc",
			"normal=hello+world&token=redacted",
		},
	} {
		t.Run(ca.name, func(t *testing.T) {
			require.Equal(t, ca.want, redactQueryString(ca.in))
		})
	}
}
