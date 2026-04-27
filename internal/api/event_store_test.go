package api //nolint:revive

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/defs"
)

func TestEventStorePublishAndSnapshot(t *testing.T) {
	s := NewEventStore(8)
	require.Equal(t, 0, s.Len())

	e1 := s.Publish(defs.EventInput{
		Kind:        "camera.online",
		Severity:    defs.EventSeverityInfo,
		SubjectKind: defs.EventSubjectKindCamera,
		SubjectID:   uuid.New().String(),
		Message:     "camera online",
	}, "rs", "tenant", "site")
	require.Equal(t, 1, s.Len())
	require.Equal(t, "camera.online", e1.Kind)
	require.Equal(t, "rs", e1.RecordingServerID)
	require.Equal(t, "tenant", e1.TenantID)
	require.Equal(t, "site", e1.SiteID)
	_, perr := uuid.Parse(e1.ID)
	require.NoError(t, perr, "publish must mint a UUID id")
	require.False(t, e1.OccurredAt.IsZero(), "BuildEvent must default OccurredAt")

	got, ok := s.GetByID(e1.ID)
	require.True(t, ok)
	require.Equal(t, e1.ID, got.ID)

	snap := s.Snapshot()
	require.Len(t, snap, 1)
	require.Equal(t, e1.ID, snap[0].ID)
}

func TestEventStoreRingBufferEviction(t *testing.T) {
	s := NewEventStore(3)
	ids := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		e := s.Publish(defs.EventInput{
			Kind:     "test.event",
			Severity: defs.EventSeverityInfo,
		}, "rs", "tenant", "site")
		ids = append(ids, e.ID)
	}
	require.Equal(t, 3, s.Len(), "ring buffer must cap at capacity")

	// First two ids should have been evicted.
	_, ok := s.GetByID(ids[0])
	require.False(t, ok, "oldest entry should be evicted")
	_, ok = s.GetByID(ids[1])
	require.False(t, ok)
	// Last three should still be present.
	for _, id := range ids[2:] {
		_, ok := s.GetByID(id)
		require.True(t, ok, "recent entry %s must remain", id)
	}
}

func TestEventStoreClear(t *testing.T) {
	s := NewEventStore(4)
	s.Publish(defs.EventInput{Kind: "k", Severity: defs.EventSeverityInfo}, "rs", "tenant", "site")
	s.Publish(defs.EventInput{Kind: "k", Severity: defs.EventSeverityInfo}, "rs", "tenant", "site")
	require.Equal(t, 2, s.Len())
	s.Clear()
	require.Equal(t, 0, s.Len())
}

func TestEventStoreDefaultCapacity(t *testing.T) {
	s := NewEventStore(0)
	require.Equal(t, defaultEventStoreCapacity, s.capacity)

	s2 := NewEventStore(-3)
	require.Equal(t, defaultEventStoreCapacity, s2.capacity)
}

func TestEventStoreSnapshotIsCopy(t *testing.T) {
	s := NewEventStore(4)
	s.Publish(defs.EventInput{Kind: "k1", Severity: defs.EventSeverityInfo}, "rs", "tenant", "site")
	snap := s.Snapshot()
	require.Len(t, snap, 1)
	// Mutate snapshot; store must remain intact.
	snap[0].Kind = "MUTATED"
	again := s.Snapshot()
	require.Equal(t, "k1", again[0].Kind)
}

func TestDefaultEventStoreSingleton(t *testing.T) {
	a := defaultEventStore()
	b := defaultEventStore()
	require.Same(t, a, b, "defaultEventStore must be a singleton")
}
