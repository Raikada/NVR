package defs

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCanonicalJSON_Determinism verifies that two AuditLogEntry
// values that differ only in map iteration order serialize to the
// same canonical bytes. The Attributes map is the most likely source
// of order non-determinism in Go.
func TestCanonicalJSON_Determinism(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)

	mk := func(attrs map[string]string) AuditLogEntry {
		return AuditLogEntry{
			ID:           "11111111-1111-1111-1111-111111111111",
			TenantID:     "t-1",
			OccurredAt:   now,
			RecordedAt:   now,
			EmitterKind:  AuditEmitterKindRecordingServer,
			EmitterID:    "rs-1",
			ActorKind:    AuditActorKindServiceAccount,
			Action:       "config.applied",
			Outcome:      AuditOutcomeSuccess,
			ResourceKind: "camera",
			Attributes:   attrs,
			PrevHash:     ZeroPrevHash,
		}
	}

	// 50 iterations: build the same logical entry with map keys
	// inserted in different orders and verify they all canonicalize
	// to the same bytes.
	first, err := CanonicalJSONAuditLogEntry(mk(map[string]string{
		"alpha":   "a",
		"bravo":   "b",
		"charlie": "c",
	}))
	require.NoError(t, err)

	for i := 0; i < 50; i++ {
		got, err := CanonicalJSONAuditLogEntry(mk(map[string]string{
			"charlie": "c",
			"alpha":   "a",
			"bravo":   "b",
		}))
		require.NoError(t, err)
		require.Equal(t, string(first), string(got),
			"canonical-JSON differed across map insertion orders")
	}

	// Sanity-check: keys are in lex order in the output.
	require.True(t, strings.Index(string(first), `"alpha"`) <
		strings.Index(string(first), `"bravo"`))
	require.True(t, strings.Index(string(first), `"bravo"`) <
		strings.Index(string(first), `"charlie"`))

	// And the top-level keys are also lex-sorted.
	require.True(t, strings.Index(string(first), `"action"`) <
		strings.Index(string(first), `"actor_kind"`))
}

// TestCanonicalJSON_HashStable verifies ComputeAuditEntryHash is
// stable across equivalent inputs and changes when content changes.
func TestCanonicalJSON_HashStable(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	base := AuditLogEntry{
		ID:           "11111111-1111-1111-1111-111111111111",
		TenantID:     "t-1",
		OccurredAt:   now,
		RecordedAt:   now,
		EmitterKind:  AuditEmitterKindRecordingServer,
		EmitterID:    "rs-1",
		ActorKind:    AuditActorKindServiceAccount,
		Action:       "config.applied",
		Outcome:      AuditOutcomeSuccess,
		ResourceKind: "camera",
		PrevHash:     ZeroPrevHash,
	}

	h1, err := ComputeAuditEntryHash(base)
	require.NoError(t, err)
	require.Len(t, h1, 64) // hex sha256 = 64 chars

	// EntryHash field is excluded from the hash (cleared before
	// canonicalization), so setting it must not change the result.
	base.EntryHash = "anything"
	h2, err := ComputeAuditEntryHash(base)
	require.NoError(t, err)
	require.Equal(t, h1, h2)

	// A real content change must move the hash.
	base2 := base
	base2.Action = "auth.login"
	h3, err := ComputeAuditEntryHash(base2)
	require.NoError(t, err)
	require.NotEqual(t, h1, h3)
}

// TestCanonicalJSON_TimestampRFC3339 verifies timestamps serialize
// in RFC 3339 form (ADR 0006 D3 rule).
func TestCanonicalJSON_TimestampRFC3339(t *testing.T) {
	entry := AuditLogEntry{
		ID:          "x",
		OccurredAt:  time.Date(2026, 4, 27, 12, 34, 56, 0, time.UTC),
		RecordedAt:  time.Date(2026, 4, 27, 12, 34, 56, 0, time.UTC),
		EmitterKind: AuditEmitterKindRecordingServer,
	}
	out, err := CanonicalJSONAuditLogEntry(entry)
	require.NoError(t, err)
	require.Contains(t, string(out), `"2026-04-27T12:34:56Z"`)
}

// TestCanonicalJSON_RoundTripThroughJSON verifies the canonical
// output is itself valid JSON that re-decodes to equivalent content.
func TestCanonicalJSON_RoundTripThroughJSON(t *testing.T) {
	now := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	entry := AuditLogEntry{
		ID:           "id-1",
		TenantID:     "t-1",
		OccurredAt:   now,
		RecordedAt:   now,
		EmitterKind:  AuditEmitterKindRecordingServer,
		EmitterID:    "rs-1",
		ActorKind:    AuditActorKindServiceAccount,
		Action:       "config.applied",
		Outcome:      AuditOutcomeSuccess,
		ResourceKind: "camera",
		Attributes:   map[string]string{"k": "v"},
		PrevHash:     ZeroPrevHash,
	}
	canon, err := CanonicalJSONAuditLogEntry(entry)
	require.NoError(t, err)

	var got AuditLogEntry
	require.NoError(t, json.Unmarshal(canon, &got))
	require.Equal(t, entry.ID, got.ID)
	require.Equal(t, entry.Action, got.Action)
	require.Equal(t, entry.Attributes, got.Attributes)
}
