package httpp

import (
	"net/http"
	"strings"

	"github.com/bluenviron/mediamtx/internal/auth"
)

// Credentials extracts credentials from a HTTP request.
//
// Two forms are accepted:
//
//   - HTTP Basic (`Authorization: Basic <base64(user:pass)>`) — sets
//     User and Pass.
//   - HTTP Bearer (`Authorization: Bearer <token>`) — sets Token. The
//     token is treated opaquely; downstream auth is responsible for
//     validating it (typically as a JWT against JWKS).
//
// The non-standard `Authorization: Bearer user:pass` form was removed
// per D6 in recorder/docs/canonical-divergences.md. Clients that
// previously relied on it should use HTTP Basic instead.
func Credentials(h *http.Request) *auth.Credentials {
	c := &auth.Credentials{}

	for _, auth := range h.Header["Authorization"] {
		if strings.HasPrefix(auth, "Bearer ") {
			c.Token = auth[len("Bearer "):]
			return c
		}
	}

	// user:pass in Authorization Basic
	c.User, c.Pass, _ = h.BasicAuth()

	return c
}
