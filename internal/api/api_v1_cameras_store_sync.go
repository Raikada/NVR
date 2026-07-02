// Package api: keep the foundation store's cameras table in step with
// the conf-path camera CRUD handlers.
//
// The base /v1/cameras handlers still operate on conf.Paths (the Phase
// 2 "full rewire" that would make cameras.Service canonical remains
// deferred), but the foundation surfaces — credential vault, health,
// PathBridge — read the store. Without this sync an API-created camera
// FK-fails on PUT /v1/cameras/:id/credentials and an API-deleted one
// leaves orphaned rows (2026-07-02 smoke findings F1/F3).
package api

import (
	"context"
	"database/sql"
	"errors"

	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/store"
)

// storeCameraFromDefs maps the canonical API entity onto the store row.
// RecordingPolicyID is intentionally left empty: the conf layer's
// policy ids (UUID scheme) and the store's seeded ids (`policy_default`)
// don't share a namespace yet; an empty value makes store readers fall
// back to policy_default (see store.Camera).
func storeCameraFromDefs(cam *defs.Camera) *store.Camera {
	return &store.Camera{
		ID:         cam.ID,
		Name:       cam.Name,
		SourceType: string(cam.SourceType),
		SourceURL:  cam.SourceURL,
		Enabled:    true,
	}
}

// syncCameraStoreCreate inserts the store row for a freshly-created
// camera. Callers must invoke it BEFORE applying the conf change so a
// store failure aborts the whole create. No-op when the cameras
// service isn't wired (legacy/test instances).
func (a *API) syncCameraStoreCreate(ctx context.Context, cam *defs.Camera) error {
	if a.CamerasService == nil {
		return nil
	}
	return a.CamerasService.Insert(ctx, storeCameraFromDefs(cam), "", "")
}

// syncCameraStoreUpdate mirrors a PATCH/PUT onto the store row. A row
// missing from the store (camera created before this sync existed, or
// declared in mediamtx.yml directly) is self-healed by inserting it.
func (a *API) syncCameraStoreUpdate(ctx context.Context, cam *defs.Camera) error {
	if a.CamerasService == nil {
		return nil
	}
	existing, err := a.CamerasService.Get(ctx, cam.ID)
	// GetByID surfaces sql.ErrNoRows rather than ErrCameraNotFound.
	if errors.Is(err, store.ErrCameraNotFound) || errors.Is(err, sql.ErrNoRows) {
		return a.CamerasService.Insert(ctx, storeCameraFromDefs(cam), "", "")
	}
	if err != nil {
		return err
	}
	existing.Name = cam.Name
	existing.SourceType = string(cam.SourceType)
	existing.SourceURL = cam.SourceURL
	return a.CamerasService.Update(ctx, existing)
}

// syncCameraStoreDelete removes the store row (cascading credentials,
// capabilities, health, events per schema) for a deleted camera. A row
// that never existed store-side is not an error.
func (a *API) syncCameraStoreDelete(ctx context.Context, id string) error {
	if a.CamerasService == nil {
		return nil
	}
	err := a.CamerasService.Delete(ctx, id)
	if errors.Is(err, store.ErrCameraNotFound) {
		return nil
	}
	return err
}
