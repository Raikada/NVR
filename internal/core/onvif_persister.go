// Adapter from internal/store.OnvifSubscriptionsRepo to the
// internal/onvif.Persister interface. Lives in core/ rather than store/
// to keep store/ from importing onvif (and vice-versa); core/ is
// already the integration layer that wires everything together.

package core

import (
	"context"

	"github.com/bluenviron/mediamtx/internal/onvif"
	recstore "github.com/bluenviron/mediamtx/internal/store"
)

type onvifSubscriptionPersister struct {
	repo *recstore.OnvifSubscriptionsRepo
}

func (p onvifSubscriptionPersister) Insert(ctx context.Context, row onvif.PersistedSubscription) error {
	return p.repo.Insert(ctx, persistedToStore(row))
}

func (p onvifSubscriptionPersister) UpdateState(ctx context.Context, row onvif.PersistedSubscription) error {
	return p.repo.UpdateState(ctx, persistedToStore(row))
}

func (p onvifSubscriptionPersister) Delete(ctx context.Context, id string) error {
	return p.repo.Delete(ctx, id)
}

func (p onvifSubscriptionPersister) ListActive(ctx context.Context) ([]onvif.PersistedSubscription, error) {
	rows, err := p.repo.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]onvif.PersistedSubscription, 0, len(rows))
	for _, r := range rows {
		out = append(out, storeToPersisted(r))
	}
	return out, nil
}

func (p onvifSubscriptionPersister) DeleteTerminated(ctx context.Context) (int, error) {
	return p.repo.DeleteTerminated(ctx)
}

func persistedToStore(row onvif.PersistedSubscription) *recstore.OnvifSubscription {
	return &recstore.OnvifSubscription{
		ID:              row.ID,
		CameraID:        row.CameraID,
		XAddr:           row.XAddr,
		Username:        row.Username,
		Password:        row.Password,
		SubscriptionURL: row.SubscriptionURL,
		TerminationTime: row.TerminationTime,
		CreatedAt:       row.CreatedAt,
		State:           row.State,
		LastError:       row.LastError,
		LastEventAt:     row.LastEventAt,
		EventCount:      row.EventCount,
	}
}

func storeToPersisted(row *recstore.OnvifSubscription) onvif.PersistedSubscription {
	return onvif.PersistedSubscription{
		ID:              row.ID,
		CameraID:        row.CameraID,
		XAddr:           row.XAddr,
		Username:        row.Username,
		Password:        row.Password,
		SubscriptionURL: row.SubscriptionURL,
		TerminationTime: row.TerminationTime,
		CreatedAt:       row.CreatedAt,
		State:           row.State,
		LastError:       row.LastError,
		LastEventAt:     row.LastEventAt,
		EventCount:      row.EventCount,
	}
}
