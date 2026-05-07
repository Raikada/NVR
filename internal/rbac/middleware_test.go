package rbac

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type fakeAudit struct {
	calls []string
}

func (f *fakeAudit) PermissionDenied(_ context.Context, c Claims, action, ip string) {
	f.calls = append(f.calls, c.Username+":"+action)
}

func TestRequirePerm_Allows(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		c.Set(ClaimsContextKey, Claims{UserID: "u1", Username: "alice", Role: RoleAdmin})
	}, RequirePerm(PermCameraCreate, nil), func(c *gin.Context) {
		c.Status(204)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	r.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

func TestRequirePerm_Denies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &fakeAudit{}
	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		c.Set(ClaimsContextKey, Claims{UserID: "u1", Username: "viewer", Role: RoleViewer})
	}, RequirePerm(PermCameraCreate, a), func(c *gin.Context) {
		c.Status(204)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	r.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Errorf("expected 403, got %d", w.Code)
	}
	if len(a.calls) != 1 {
		t.Errorf("expected 1 audit call, got %d", len(a.calls))
	}
}

func TestRequirePerm_Anonymous(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", RequirePerm(PermCameraCreate, nil), func(c *gin.Context) {
		c.Status(204)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestRequirePerm_BadClaimsShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		c.Set(ClaimsContextKey, "not-claims")
	}, RequirePerm(PermCameraCreate, nil), func(c *gin.Context) {
		c.Status(204)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("expected 401, got %d", w.Code)
	}
}
