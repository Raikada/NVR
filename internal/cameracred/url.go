package cameracred

import (
	"net/url"
	"strings"
)

// MaterializeRTSPURL injects username + password into an RTSP URL
// template (no userinfo). The result is the only point where plaintext
// password meets a string. Callers must NEVER log this value.
//
// Example:
//   tmpl = "rtsp://192.168.1.42/cam/realmonitor?channel=1&subtype=0"
//   user = "admin", pass = "p@ss/word"
//   ->     "rtsp://admin:p%40ss%2Fword@192.168.1.42/cam/realmonitor?channel=1&subtype=0"
//
// If the template already contains userinfo, MaterializeRTSPURL replaces
// it. If parsing fails, the template is returned unchanged (so callers
// can fall back to the unmaterialized URL when they have no credentials
// configured); callers should validate beforehand for production use.
func MaterializeRTSPURL(tmpl, username, password string) string {
	if username == "" {
		return tmpl
	}
	u, err := url.Parse(tmpl)
	if err != nil {
		return tmpl
	}
	u.User = url.UserPassword(username, password)
	return u.String()
}

// RedactURLUserinfo returns a copy of an RTSP URL with userinfo removed,
// suitable for logging. Used by the auth/credentials redaction helper.
// Inputs that don't look like URLs (no "://") are returned verbatim.
func RedactURLUserinfo(raw string) string {
	if !strings.Contains(raw, "://") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		// Best-effort: blanket strip "user:pass@" from rtsp(s)://
		if i := strings.Index(raw, "://"); i >= 0 {
			rest := raw[i+3:]
			if at := strings.Index(rest, "@"); at >= 0 {
				return raw[:i+3] + rest[at+1:]
			}
		}
		return raw
	}
	if u.User != nil {
		u.User = nil
	}
	return u.String()
}
