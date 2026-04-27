package api //nolint:revive

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/defs"
)

// TestAuditChain_LinksSequential verifies that consecutive entries
// chain through prev_hash -> entry_hash, and the first entry's
// prev_hash is the zero sentinel.
func TestAuditChain_LinksSequential(t *testing.T) {
	buf := NewAuditBuffer(16)
	chain := NewAuditChain(defs.AuditEmitterKindRecordingServer, "rs-1", buf)

	e1, err := chain.Append(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindServiceAccount,
		Action:       "config.applied",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "camera",
		ResourceID:   "cam-1",
	}, "tenant-1", "")
	require.NoError(t, err)
	require.Equal(t, defs.ZeroPrevHash, e1.PrevHash, "first entry must have zero prev_hash")
	require.NotEmpty(t, e1.EntryHash)
	require.Equal(t, defs.AuditEmitterKindRecordingServer, e1.EmitterKind)
	require.Equal(t, "rs-1", e1.EmitterID)
	require.Equal(t, "tenant-1", e1.TenantID)

	e2, err := chain.Append(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindServiceAccount,
		Action:       "config.applied",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "camera",
		ResourceID:   "cam-2",
	}, "tenant-1", "")
	require.NoError(t, err)
	require.Equal(t, e1.EntryHash, e2.PrevHash, "e2.prev_hash must equal e1.entry_hash")
	require.NotEqual(t, e1.EntryHash, e2.EntryHash)

	e3, err := chain.Append(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindServiceAccount,
		Action:       "auth.login",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "session",
	}, "tenant-1", "")
	require.NoError(t, err)
	require.Equal(t, e2.EntryHash, e3.PrevHash)

	require.Equal(t, e3.EntryHash, chain.HeadHash())
	require.Equal(t, 3, buf.Len())
}

// TestAuditChain_HashVerifiable verifies that EntryHash recomputes
// over the canonical-JSON serialization of the entry as written.
func TestAuditChain_HashVerifiable(t *testing.T) {
	buf := NewAuditBuffer(16)
	chain := NewAuditChain(defs.AuditEmitterKindRecordingServer, "rs-1", buf)

	e, err := chain.Append(defs.AuditLogEntryInput{
		ActorKind:    defs.AuditActorKindLocalUser,
		ActorID:      "u-1",
		Action:       "auth.login",
		Outcome:      defs.AuditOutcomeSuccess,
		ResourceKind: "session",
		SourceIP:     "192.0.2.5",
	}, "tenant-1", "")
	require.NoError(t, err)

	want, err := defs.ComputeAuditEntryHash(e)
	require.NoError(t, err)
	require.Equal(t, want, e.EntryHash)
}

// TestAuditBuffer_RingEviction verifies oldest-first eviction at
// capacity. The chain head continues advancing past evicted entries;
// the cryptographic chain remains intact even when the buffer drops
// the older entries from view.
func TestAuditBuffer_RingEviction(t *testing.T) {
	buf := NewAuditBuffer(3)
	chain := NewAuditChain(defs.AuditEmitterKindRecordingServer, "rs-1", buf)

	for i := 0; i < 5; i++ {
		_, err := chain.Append(defs.AuditLogEntryInput{
			ActorKind:    defs.AuditActorKindServiceAccount,
			Action:       "config.applied",
			Outcome:      defs.AuditOutcomeSuccess,
			ResourceKind: "camera",
		}, "tenant-1", "")
		require.NoError(t, err)
	}
	require.Equal(t, 3, buf.Len(), "buffer must cap at capacity=3")

	// The head hash is the 5th entry's hash. The buffer's first item
	// is now the 3rd entry's hash (entries 1 and 2 were evicted), but
	// the chain still links: buffer items must form a valid prev/entry
	// chain.
	snap := buf.Snapshot()
	require.Equal(t, 3, len(snap))
	for i := 1; i < len(snap); i++ {
		require.Equal(t, snap[i-1].EntryHash, snap[i].PrevHash,
			"chain must remain intact after eviction")
	}
}

// TestAuditBuffer_DegradedMode verifies the 80% threshold semantics.
func TestAuditBuffer_DegradedMode(t *testing.T) {
	buf := NewAuditBuffer(10)
	require.False(t, buf.IsDegraded(), "empty buffer is not degraded")

	// Fill to 7 (70%) — below threshold.
	for i := 0; i < 7; i++ {
		buf.Append(defs.AuditLogEntry{ID: "x"})
	}
	require.False(t, buf.IsDegraded(), "70%% must not be degraded")

	// Add one more to reach 8 (80%) — at threshold.
	buf.Append(defs.AuditLogEntry{ID: "x"})
	require.True(t, buf.IsDegraded(), "80%% must be degraded")

	// Continues past threshold.
	buf.Append(defs.AuditLogEntry{ID: "x"})
	require.True(t, buf.IsDegraded(), "above threshold remains degraded")
}

// TestAuditEmitDecision_PopulatesActorAndOutcome verifies the
// emitAuthDecision wrapper produces an entry with actor/outcome set
// correctly for both success and failure.
func TestAuditEmitDecision_PopulatesActorAndOutcome(t *testing.T) {
	defaultAuditBuffer().Clear()
	defaultAuditChain().resetForTests()
	t.Cleanup(func() {
		defaultAuditBuffer().Clear()
		defaultAuditChain().resetForTests()
	})

	cnf := tempConf(t, "api: yes\n")
	a := &API{Conf: cnf}

	a.emitAuthDecision(
		defs.AuditOutcomeFailure,
		defs.AuditActorKindUnauthenticated,
		"",
		"192.0.2.5",
		"",
		map[string]string{"reason": "bad creds"},
	)
	a.emitAuthDecision(
		defs.AuditOutcomeSuccess,
		defs.AuditActorKindServiceAccount,
		"",
		"192.0.2.6",
		"",
		nil,
	)

	snap := defaultAuditBuffer().Snapshot()
	require.Len(t, snap, 2)
	require.Equal(t, defs.AuditOutcomeFailure, snap[0].Outcome)
	require.Equal(t, "auth.failed_login", snap[0].Action)
	require.Equal(t, "192.0.2.5", snap[0].SourceIP)
	require.Equal(t, defs.AuditOutcomeSuccess, snap[1].Outcome)
	require.Equal(t, "auth.login", snap[1].Action)

	// Chain must link the two entries.
	require.Equal(t, snap[0].EntryHash, snap[1].PrevHash)
}
