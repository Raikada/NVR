package policysync

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
	require.Equal(t, identity.CanonicalSourceRecorder, id.PolicyCanonicalSource())

	// User principal — pre-import, no lockdown.
	dec := Gate(id, &fakePrincipal{service: false}, false)
	require.Equal(t, DecisionAllow, dec)
}

func TestGate_MSCanonical_RejectsNonServicePrincipal(t *testing.T) {
	id := openIdentity(t)
	require.NoError(t, id.SetPolicyCanonicalSource(identity.CanonicalSourceMS))
	require.Equal(t, identity.CanonicalSourceMS, id.PolicyCanonicalSource())

	// Operator JWT (non-service) without override: rejected.
	dec := Gate(id, &fakePrincipal{service: false}, false)
	require.Equal(t, DecisionRejectMSCanonical, dec)
}

func TestGate_MSCanonical_AdmitsServicePrincipalWithScope(t *testing.T) {
	id := openIdentity(t)
	require.NoError(t, id.SetPolicyCanonicalSource(identity.CanonicalSourceMS))

	dec := Gate(id, &fakePrincipal{service: true, scopes: []string{"recording_policy.push"}}, false)
	require.Equal(t, DecisionAllow, dec)
}

func TestGate_MSCanonical_RejectsServicePrincipalWithCameraScopeOnly(t *testing.T) {
	// Per ADR 0017 OQ2 (i): the recording-policy lockdown gate admits
	// only the recording_policy.push scope. A service JWT carrying
	// only camera.push (slice 4-B) does NOT pass this gate.
	id := openIdentity(t)
	require.NoError(t, id.SetPolicyCanonicalSource(identity.CanonicalSourceMS))

	dec := Gate(id, &fakePrincipal{service: true, scopes: []string{"camera.push"}}, false)
	require.Equal(t, DecisionRejectMSCanonical, dec)
}

func TestGate_MSCanonical_RejectsServicePrincipalWithoutScope(t *testing.T) {
	id := openIdentity(t)
	require.NoError(t, id.SetPolicyCanonicalSource(identity.CanonicalSourceMS))

	dec := Gate(id, &fakePrincipal{service: true, scopes: nil}, false)
	require.Equal(t, DecisionRejectMSCanonical, dec)
}

func TestGate_MSCanonical_AdmitsBreakglass(t *testing.T) {
	id := openIdentity(t)
	require.NoError(t, id.SetPolicyCanonicalSource(identity.CanonicalSourceMS))

	dec := Gate(id, &fakePrincipal{service: false}, true)
	require.Equal(t, DecisionAllowBreakglass, dec)
}

func TestEngageLockdown_OneWayAndIdempotent(t *testing.T) {
	id := openIdentity(t)
	require.Equal(t, identity.CanonicalSourceRecorder, id.PolicyCanonicalSource())

	require.NoError(t, EngageLockdown(id))
	require.Equal(t, identity.CanonicalSourceMS, id.PolicyCanonicalSource())

	// Second call: idempotent.
	require.NoError(t, EngageLockdown(id))
	require.Equal(t, identity.CanonicalSourceMS, id.PolicyCanonicalSource())
}

func TestIdentity_PolicyCanonicalSourcePersists(t *testing.T) {
	dir := t.TempDir()
	id, err := identity.Open(dir)
	require.NoError(t, err)
	require.NoError(t, id.SetPolicyCanonicalSource(identity.CanonicalSourceMS))

	id2, err := identity.Open(dir)
	require.NoError(t, err)
	require.Equal(t, identity.CanonicalSourceMS, id2.PolicyCanonicalSource())
}

func TestIdentity_PerEntityClassFlagsAreIndependent(t *testing.T) {
	// ADR 0017 D3 + ADR 0009 §D2 amendment: per-entity-class flags.
	// A recorder may legitimately be camera-MS and policy-recorder
	// simultaneously during the 4-B → 4-C migration window.
	dir := t.TempDir()
	id, err := identity.Open(dir)
	require.NoError(t, err)

	require.NoError(t, id.SetCanonicalSource(identity.CanonicalSourceMS))
	require.Equal(t, identity.CanonicalSourceMS, id.CanonicalSource())
	require.Equal(t, identity.CanonicalSourceRecorder, id.PolicyCanonicalSource())

	require.NoError(t, id.SetPolicyCanonicalSource(identity.CanonicalSourceMS))
	require.Equal(t, identity.CanonicalSourceMS, id.CanonicalSource())
	require.Equal(t, identity.CanonicalSourceMS, id.PolicyCanonicalSource())
}

func TestIdentity_ClearIssuedIdentity_ResetsBothFlags(t *testing.T) {
	dir := t.TempDir()
	id, err := identity.Open(dir)
	require.NoError(t, err)
	require.NoError(t, id.SetCanonicalSource(identity.CanonicalSourceMS))
	require.NoError(t, id.SetPolicyCanonicalSource(identity.CanonicalSourceMS))

	require.NoError(t, id.ClearIssuedIdentity())
	require.Equal(t, identity.CanonicalSourceRecorder, id.CanonicalSource())
	require.Equal(t, identity.CanonicalSourceRecorder, id.PolicyCanonicalSource())
}
