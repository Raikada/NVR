package api //nolint:revive

import (
	"net/url"
	"strings"
)

// redactSourceURL returns the input with any embedded userinfo
// (`user:pass@host`) replaced by `redacted:redacted@host`. Other URL
// components pass through unchanged. Inputs that don't look like URLs
// (no scheme, no userinfo) pass through unchanged. See D4 in
// recorder/docs/canonical-divergences.md.
//
// This is the response-side defense-in-depth fix: the recorder still
// accepts source URLs with embedded credentials on writes; it just
// doesn't return them. The full canonical fix (split into
// `Camera.source_url` + `Camera.credentials_ref`) lands with ADR 0009.
func redactSourceURL(s string) string {
	if s == "" || !strings.Contains(s, "@") {
		return s
	}
	u, err := url.Parse(s)
	if err != nil || u.User == nil {
		return s
	}
	u.User = url.UserPassword("redacted", "redacted")
	return u.String()
}
