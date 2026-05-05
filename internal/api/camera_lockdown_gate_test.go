package api //nolint:revive

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/identity"
)

// openMSCanonicalIdentity opens a fresh identity in a temp dir and
// flips it to canonical_source = ms — i.e., the recorder is locked
// down per ADR 0016 D5.
func openMSCanonicalIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.Open(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, id.SetCanonicalSource(identity.CanonicalSourceMS))
	return id
}

func openRecorderCanonicalIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	id, err := identity.Open(t.TempDir())
	require.NoError(t, err)
	return id
}

func TestApplyCameraLockdown_PreImport_AllowsAnyone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &API{Identity: openRecorderCanonicalIdentity(t)}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/cameras", nil)
	// No principal in context: defaults to unauthenticated.
	require.True(t, a.applyCameraLockdown(c, "", "create"))
}

func TestApplyCameraLockdown_PostImport_RejectsUserPrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &API{Identity: openMSCanonicalIdentity(t)}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/cameras", nil)
	setPrincipalOnContext(c, &Principal{
		Sub:           "operator-1",
		PrincipalKind: defs.AuditActorKindLocalUser,
	})
	require.False(t, a.applyCameraLockdown(c, "", "create"))
	require.Equal(t, http.StatusForbidden, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, ErrCodeCameraCanonicalSourceIsMS, body["code"])
}

func TestApplyCameraLockdown_PostImport_AdmitsServiceAccountWithCameraPushScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &API{Identity: openMSCanonicalIdentity(t)}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/cameras", nil)
	setPrincipalOnContext(c, &Principal{
		Sub:           "ms-service-camera-push",
		PrincipalKind: defs.AuditActorKindServiceAccount,
		Scope:         []string{"camera.push"},
	})
	require.True(t, a.applyCameraLockdown(c, "", "create"))
	require.NotEqual(t, http.StatusForbidden, w.Code)
}

func TestApplyCameraLockdown_PostImport_RejectsServiceAccountWithoutScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &API{Identity: openMSCanonicalIdentity(t)}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/cameras", nil)
	setPrincipalOnContext(c, &Principal{
		Sub:           "some-other-service",
		PrincipalKind: defs.AuditActorKindServiceAccount,
		// Scope intentionally empty — not the camera-push principal.
	})
	require.False(t, a.applyCameraLockdown(c, "", "create"))
	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestApplyCameraLockdown_PostImport_AdmitsBreakglass(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &API{Identity: openMSCanonicalIdentity(t)}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/cameras", nil)
	c.Request.Header.Set(HeaderLocalAdminOverride, "true")
	setPrincipalOnContext(c, &Principal{
		Sub:           "operator-1",
		PrincipalKind: defs.AuditActorKindLocalUser,
	})
	require.True(t, a.applyCameraLockdown(c, "cam-id", "delete"))
}

func TestEngageLockdownIfMSSourced_Idempotent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id := openRecorderCanonicalIdentity(t)
	a := &API{Identity: id}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/cameras", nil)
	setPrincipalOnContext(c, &Principal{
		PrincipalKind: defs.AuditActorKindServiceAccount,
		Scope:         []string{"camera.push"},
	})
	a.engageLockdownIfMSSourced(c)
	require.Equal(t, identity.CanonicalSourceMS, id.CanonicalSource())
	a.engageLockdownIfMSSourced(c)
	require.Equal(t, identity.CanonicalSourceMS, id.CanonicalSource())
}

func TestEngageLockdownIfMSSourced_NoOpForUserPrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id := openRecorderCanonicalIdentity(t)
	a := &API{Identity: id}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/cameras", nil)
	setPrincipalOnContext(c, &Principal{
		Sub:           "operator-1",
		PrincipalKind: defs.AuditActorKindLocalUser,
	})
	a.engageLockdownIfMSSourced(c)
	require.Equal(t, identity.CanonicalSourceRecorder, id.CanonicalSource())
}
