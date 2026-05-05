package camerasync

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/identity"
)

type fakePrincipal struct {
	service bool
	scopes  []string
}

func (f *fakePrincipal) IsServiceAccount() bool { return f.service }
func (f *fakePrincipal) HasPermission(perm string) bool {
	for _, s := range f.scopes {
		if s == perm {
			return true
		}
	}
	return false
}

func openIdentity(t *testing.T) *identity.Identity {
	t.Helper()
	dir := t.TempDir()
	id, err := identity.Open(dir)
	require.NoError(t, err)
	return id
}

func TestGate_NilIdentity_AllowsByDefault(t *testing.T) {
	dec := Gate(nil, &fakePrincipal{}, false)
	require.Equal(t, DecisionAllow, dec)
}

func TestGate_RecorderCanonical_AllowsAnyone(t *testing.T) {
	id := openIdentity(t)
	require.Equal(t, identity.CanonicalSourceRecorder, id.CanonicalSource())

	// User principal — pre-import, no lockdown.
	dec := Gate(id, &fakePrincipal{service: false}, false)
	require.Equal(t, DecisionAllow, dec)
}

func TestGate_MSCanonical_RejectsNonServicePrincipal(t *testing.T) {
	id := openIdentity(t)
	require.NoError(t, id.SetCanonicalSource(identity.CanonicalSourceMS))
	require.Equal(t, identity.CanonicalSourceMS, id.CanonicalSource())

	// Operator JWT (non-service) without override: rejected.
	dec := Gate(id, &fakePrincipal{service: false}, false)
	require.Equal(t, DecisionRejectMSCanonical, dec)
}

func TestGate_MSCanonical_AdmitsServicePrincipalWithScope(t *testing.T) {
	id := openIdentity(t)
	require.NoError(t, id.SetCanonicalSource(identity.CanonicalSourceMS))

	dec := Gate(id, &fakePrincipal{service: true, scopes: []string{"camera.push"}}, false)
	require.Equal(t, DecisionAllow, dec)
}

func TestGate_MSCanonical_RejectsServicePrincipalWithoutScope(t *testing.T) {
	id := openIdentity(t)
	require.NoError(t, id.SetCanonicalSource(identity.CanonicalSourceMS))

	dec := Gate(id, &fakePrincipal{service: true, scopes: nil}, false)
	require.Equal(t, DecisionRejectMSCanonical, dec)
}

func TestGate_MSCanonical_AdmitsBreakglass(t *testing.T) {
	id := openIdentity(t)
	require.NoError(t, id.SetCanonicalSource(identity.CanonicalSourceMS))

	dec := Gate(id, &fakePrincipal{service: false}, true)
	require.Equal(t, DecisionAllowBreakglass, dec)
}

func TestEngageLockdown_OneWayAndIdempotent(t *testing.T) {
	id := openIdentity(t)
	require.Equal(t, identity.CanonicalSourceRecorder, id.CanonicalSource())

	require.NoError(t, EngageLockdown(id))
	require.Equal(t, identity.CanonicalSourceMS, id.CanonicalSource())

	// Second call: idempotent.
	require.NoError(t, EngageLockdown(id))
	require.Equal(t, identity.CanonicalSourceMS, id.CanonicalSource())
}

func TestIdentity_CanonicalSourcePersists(t *testing.T) {
	dir := t.TempDir()
	id, err := identity.Open(dir)
	require.NoError(t, err)
	require.NoError(t, id.SetCanonicalSource(identity.CanonicalSourceMS))

	id2, err := identity.Open(dir)
	require.NoError(t, err)
	require.Equal(t, identity.CanonicalSourceMS, id2.CanonicalSource())
}

func TestIdentity_ClearIssuedIdentity_ResetsCanonicalSource(t *testing.T) {
	dir := t.TempDir()
	id, err := identity.Open(dir)
	require.NoError(t, err)
	require.NoError(t, id.SetCanonicalSource(identity.CanonicalSourceMS))

	require.NoError(t, id.ClearIssuedIdentity())
	require.Equal(t, identity.CanonicalSourceRecorder, id.CanonicalSource())
}
