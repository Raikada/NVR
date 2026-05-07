package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOnvifSubscriptionsInsertGetList(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Millisecond)
	sub := &OnvifSubscription{
		ID:              "sub-1",
		CameraID:        "cam-1",
		XAddr:           "http://camera/onvif/event_service",
		Username:        "admin",
		Password:        "secret",
		SubscriptionURL: "http://camera/onvif/sub/abc",
		TerminationTime: now.Add(5 * time.Minute),
		CreatedAt:       now,
		State:           "active",
	}
	require.NoError(t, s.OnvifSubscriptions.Insert(ctx, sub))

	got, err := s.OnvifSubscriptions.ListActive(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "sub-1", got[0].ID)
	require.Equal(t, "cam-1", got[0].CameraID)
	require.Equal(t, "secret", got[0].Password)
	require.Equal(t, "http://camera/onvif/sub/abc", got[0].SubscriptionURL)
	require.True(t, got[0].TerminationTime.Equal(sub.TerminationTime))
}

func TestOnvifSubscriptionsUpdateState(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	sub := &OnvifSubscription{
		ID:              "sub-2",
		CameraID:        "cam-2",
		XAddr:           "http://camera/onvif/event_service",
		SubscriptionURL: "http://camera/onvif/sub/xyz",
		TerminationTime: now.Add(5 * time.Minute),
		CreatedAt:       now,
		State:           "active",
	}
	require.NoError(t, s.OnvifSubscriptions.Insert(ctx, sub))

	sub.State = "failed"
	sub.LastError = "renew failed: HTTP 500"
	sub.EventCount = 7
	sub.LastEventAt = now.Add(time.Minute)
	require.NoError(t, s.OnvifSubscriptions.UpdateState(ctx, sub))

	all, err := s.OnvifSubscriptions.ListAll(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, "failed", all[0].State)
	require.Equal(t, "renew failed: HTTP 500", all[0].LastError)
	require.Equal(t, 7, all[0].EventCount)
	require.True(t, all[0].LastEventAt.Equal(sub.LastEventAt))

	// ListActive must skip the now-failed row.
	active, err := s.OnvifSubscriptions.ListActive(ctx)
	require.NoError(t, err)
	require.Empty(t, active)
}

func TestOnvifSubscriptionsDeleteAndDeleteTerminated(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)

	mk := func(id, state string) *OnvifSubscription {
		return &OnvifSubscription{
			ID:              id,
			CameraID:        "cam-" + id,
			XAddr:           "http://camera",
			SubscriptionURL: "http://camera/sub/" + id,
			TerminationTime: now.Add(5 * time.Minute),
			CreatedAt:       now,
			State:           state,
		}
	}
	require.NoError(t, s.OnvifSubscriptions.Insert(ctx, mk("a", "active")))
	require.NoError(t, s.OnvifSubscriptions.Insert(ctx, mk("b", "failed")))
	require.NoError(t, s.OnvifSubscriptions.Insert(ctx, mk("c", "terminated")))

	pruned, err := s.OnvifSubscriptions.DeleteTerminated(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, pruned)

	require.NoError(t, s.OnvifSubscriptions.Delete(ctx, "a"))
	require.True(t, errors.Is(s.OnvifSubscriptions.Delete(ctx, "missing"), ErrOnvifSubscriptionNotFound))

	all, err := s.OnvifSubscriptions.ListAll(ctx)
	require.NoError(t, err)
	require.Empty(t, all)
}

func TestOnvifSubscriptionsUpdateStateMissing(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	err := s.OnvifSubscriptions.UpdateState(ctx, &OnvifSubscription{
		ID:    "ghost",
		State: "active",
	})
	require.True(t, errors.Is(err, ErrOnvifSubscriptionNotFound))
}
