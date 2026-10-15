package amcrestchannel

import (
	"crypto/md5" //nolint:gosec // RFC 2617 digest auth requires MD5; camera-dictated.
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// digestTransport implements RFC 2617 HTTP digest auth as Amcrest
// cameras speak it (MD5, qop=auth). Semantics ported from the
// amcrest-sdk project (Apache-2.0), reimplemented here.
type digestTransport struct {
	username string
	password string
	base     http.RoundTripper

	mu    sync.Mutex
	realm string
	nonce string
	qop   string
	nc    int
}

// NewDigestTransport is exported for sibling packages that must speak
// digest auth to the same cameras (snapshot fetch, SP4).
func NewDigestTransport(username, password string, base http.RoundTripper) *digestTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &digestTransport{username: username, password: password, base: base}
}

// RoundTrip performs the request, answering one 401 challenge with the
// computed Authorization header. Long-lived streaming responses pass
// straight through once authorized.
func (d *digestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Reuse a known-good challenge first to avoid a 401 round-trip per
	// request (the camera keeps nonces valid across requests).
	if h := d.authHeader(req.Method, req.URL.RequestURI()); h != "" {
		req.Header.Set("Authorization", h)
	}
	resp, err := d.base.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	// Challenge: parse, close the 401 body, retry once.
	challenge := resp.Header.Get("WWW-Authenticate")
	resp.Body.Close()
	if !strings.HasPrefix(strings.ToLower(challenge), "digest ") {
		return nil, fmt.Errorf("camera sent non-digest challenge: %.60s", challenge)
	}
	d.storeChallenge(challenge)

	retry := req.Clone(req.Context())
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		retry.Body = body
	}
	h := d.authHeader(retry.Method, retry.URL.RequestURI())
	if h == "" {
		return nil, fmt.Errorf("digest challenge missing realm/nonce")
	}
	retry.Header.Set("Authorization", h)
	return d.base.RoundTrip(retry)
}

func (d *digestTransport) storeChallenge(challenge string) {
	params := map[string]string{}
	for _, part := range strings.Split(challenge[len("Digest "):], ",") {
		part = strings.TrimSpace(part)
		eq := strings.Index(part, "=")
		if eq < 0 {
			continue
		}
		params[strings.ToLower(part[:eq])] = strings.Trim(part[eq+1:], `"`)
	}
	d.mu.Lock()
	d.realm = params["realm"]
	d.nonce = params["nonce"]
	d.qop = params["qop"]
	d.nc = 0
	d.mu.Unlock()
}

func (d *digestTransport) authHeader(method, uri string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.realm == "" || d.nonce == "" {
		return ""
	}
	d.nc++
	nc := fmt.Sprintf("%08x", d.nc)
	cnonce := randomHex(8)

	ha1 := md5hex(d.username + ":" + d.realm + ":" + d.password)
	ha2 := md5hex(method + ":" + uri)

	var response string
	if strings.Contains(d.qop, "auth") {
		response = md5hex(strings.Join([]string{ha1, d.nonce, nc, cnonce, "auth", ha2}, ":"))
		return fmt.Sprintf(
			`Digest username="%s", realm="%s", nonce="%s", uri="%s", qop=auth, nc=%s, cnonce="%s", response="%s"`,
			d.username, d.realm, d.nonce, uri, nc, cnonce, response)
	}
	response = md5hex(ha1 + ":" + d.nonce + ":" + ha2)
	return fmt.Sprintf(
		`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`,
		d.username, d.realm, d.nonce, uri, response)
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s)) //nolint:gosec
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
