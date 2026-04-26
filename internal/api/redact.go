package api //nolint:revive

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// queryRedactPattern matches query-string keys whose values must be
// redacted on output: any token, password, key, or secret variant.
// Case-insensitive. Substring match — `access_token`, `apikey`,
// `client_secret`, `auth-password` all hit.
var queryRedactPattern = regexp.MustCompile(`(?i)token|password|key|secret`)

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

// redactQueryString returns the input with the values of any query-
// string keys matching `token`, `password`, `key`, or `secret` (case-
// insensitive, substring match) replaced by the literal `redacted`.
// Other keys and values pass through unchanged. The keys themselves
// remain visible — the redaction is on values only, so reviewers can
// still see that a sensitive parameter was present without seeing
// what it was.
//
// Inputs that don't parse as a URL query string pass through
// unchanged. See D12 in recorder/docs/canonical-divergences.md.
func redactQueryString(q string) string {
	if q == "" {
		return q
	}
	values, err := url.ParseQuery(q)
	if err != nil {
		return q
	}
	dirty := false
	for k, vs := range values {
		if !queryRedactPattern.MatchString(k) {
			continue
		}
		for i := range vs {
			vs[i] = "redacted"
		}
		values[k] = vs
		dirty = true
	}
	if !dirty {
		return q
	}
	// Preserve the original key order is not strictly possible after
	// url.ParseQuery (which uses a map). For our purposes alphabetical
	// is fine — the values are what matter for forensic review, not
	// parameter order.
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic output
	var b strings.Builder
	for _, k := range keys {
		for _, v := range values[k] {
			if b.Len() > 0 {
				b.WriteByte('&')
			}
			b.WriteString(url.QueryEscape(k))
			b.WriteByte('=')
			b.WriteString(url.QueryEscape(v))
		}
	}
	return b.String()
}

