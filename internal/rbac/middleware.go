package rbac

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

// ClaimsContextKey is the gin context key the auth middleware uses to
// stash the authenticated user's claims. Defined here so the rbac
// middleware does not pull in internal/auth (and vice versa).
const ClaimsContextKey = "raikada.claims"

// Claims is the minimal shape the rbac middleware reads. The auth layer
// constructs it from the validated JWT.
type Claims struct {
	UserID   string
	Username string
	Role     Role
}

// AuditEmitter is the per-request audit hook. Implemented by the audit
// emitter wired in Phase 6; passed in by reference so this package does
// not pull in internal/store. nil means no audit emission (e.g., tests).
type AuditEmitter interface {
	PermissionDenied(ctx context.Context, claims Claims, action, ip string)
}

// RequirePerm returns a Gin middleware that aborts with 403 if the
// authenticated user does not have the named permission. Anonymous
// requests are aborted with 401.
func RequirePerm(perm Permission, audit AuditEmitter) gin.HandlerFunc {
	return func(c *gin.Context) {
		v, ok := c.Get(ClaimsContextKey)
		if !ok {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		claims, ok := v.(Claims)
		if !ok {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		if !HasPermission(claims.Role, perm) {
			if audit != nil {
				audit.PermissionDenied(c.Request.Context(), claims, c.Request.URL.Path, c.ClientIP())
			}
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}
