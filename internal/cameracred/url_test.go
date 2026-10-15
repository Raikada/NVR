package cameracred

import "testing"

func TestMaterializeRTSPURL(t *testing.T) {
	cases := []struct {
		name     string
		tmpl     string
		user     string
		pass     string
		expected string
	}{
		{
			name:     "simple",
			tmpl:     "rtsp://192.168.1.42/cam",
			user:     "admin",
			pass:     "p4ss",
			expected: "rtsp://admin:p4ss@192.168.1.42/cam",
		},
		{
			name:     "password with reserved chars is escaped",
			tmpl:     "rtsp://host/path",
			user:     "u",
			pass:     "p@ss/word",
			expected: "rtsp://u:p%40ss%2Fword@host/path",
		},
		{
			name:     "no user returns template unchanged",
			tmpl:     "rtsp://host/path",
			user:     "",
			expected: "rtsp://host/path",
		},
		{
			name:     "preserves query string",
			tmpl:     "rtsp://host/cam?channel=1",
			user:     "admin",
			pass:     "p",
			expected: "rtsp://admin:p@host/cam?channel=1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MaterializeRTSPURL(tc.tmpl, tc.user, tc.pass)
			if got != tc.expected {
				t.Errorf("got %q want %q", got, tc.expected)
			}
		})
	}
}

func TestRedactURLUserinfo(t *testing.T) {
	cases := []struct {
		raw, want string
	}{
		{"rtsp://admin:p4ss@host/path", "rtsp://host/path"},
		{"rtsps://u:p%40ss@host:554/cam?x=1", "rtsps://host:554/cam?x=1"},
		{"http://host/path", "http://host/path"},
		{"not a url", "not a url"},
	}
	for _, tc := range cases {
		got := RedactURLUserinfo(tc.raw)
		if got != tc.want {
			t.Errorf("redact(%q): got %q want %q", tc.raw, got, tc.want)
		}
	}
}
