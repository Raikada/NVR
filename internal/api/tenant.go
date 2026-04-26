package api //nolint:revive

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// tenantID returns the recorder's currently-configured tenant id, taking
// the standard mutex on a.Conf. See ADR 0005 D7 for why every recorder
// is bound to exactly one tenant; D1 in canonical-divergences.md for the
// API-surface implications of that binding.
//
// Returns the empty string if a.Conf is nil — this case occurs only in
// tests that construct the API struct directly without a Conf, which
// pre-date D1 and don't need a tenant id to validate handler behavior.
// Production callers always have Conf set by Initialize.
func (a *API) tenantID() string {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	if a.Conf == nil {
		return ""
	}
	return a.Conf.TenantID
}

// readTenantScopedBody reads the request body up to maxInboundConfigSize
// and validates that any tenantId field included in the JSON matches the
// recorder's bound tenant. On mismatch, it writes a 403 and returns
// (nil, false). On read error, it writes a 400 and returns (nil, false).
// On success, it returns the body bytes for the caller to decode itself.
//
// A request body without a tenantId field passes through unchanged — the
// recorder uses its own tenant as the implicit value (per the D1
// resolution).
func (a *API) readTenantScopedBody(ctx *gin.Context) ([]byte, bool) {
	body, err := io.ReadAll(&customLimitReader{ctx.Request.Body, maxInboundConfigSize})
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return nil, false
	}

	// We only check tenantId here; if the JSON is otherwise malformed,
	// the caller's full decode will surface the parse error.
	var meta struct {
		TenantID *string `json:"tenantId"`
	}
	if jerr := json.Unmarshal(body, &meta); jerr == nil &&
		meta.TenantID != nil && *meta.TenantID != a.tenantID() {
		a.writeError(ctx, http.StatusForbidden,
			fmt.Errorf("tenant_id mismatch: recorder is bound to a different tenant"))
		return nil, false
	}

	return body, true
}
