package api //nolint:revive

import (
	"context"
	"fmt"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/store"
)

// ReconcileStoreCameras folds camera store rows missing from the conf
// back into it as paths, with each camera's RecordingPolicy applied —
// the same projection POST /v1/cameras performs at create time.
//
// The store is the durable source of truth for cameras; the conf file
// is a derived projection that can be lost when the recorder's config
// is replaced (e.g. a container upgrade ships a pristine config while
// /data keeps the database). Without this reconcile the recorder keeps
// streaming the store-backed paths via the path bridge, but
// GET /v1/cameras — which lists conf paths — reports an empty fleet.
//
// Mutates cnf in place. Returns the number of paths added. Callers
// persist the conf when added > 0.
func ReconcileStoreCameras(ctx context.Context, cnf *conf.Conf, st *store.Store) (int, error) {
	if cnf == nil || st == nil {
		return 0, nil
	}
	rows, err := st.Cameras.List(ctx, store.ListCamerasFilter{Limit: 500})
	if err != nil {
		return 0, fmt.Errorf("list cameras: %w", err)
	}

	added := 0
	for _, row := range rows {
		if row.Name == "" {
			continue
		}
		if _, exists := cnf.OptionalPaths[row.Name]; exists {
			continue
		}
		if _, exists := cnf.Paths[row.Name]; exists {
			continue
		}

		policyID := row.RecordingPolicyID
		if policyID == "" {
			policyID = conf.DefaultRecordingPolicyID
		}
		policyCfg := cnf.RecordingPolicies[policyID]
		if policyCfg == nil {
			// Unknown policy (e.g. deleted): fall back to Default so the
			// camera comes back recording rather than being dropped.
			policyID = conf.DefaultRecordingPolicyID
			policyCfg = cnf.RecordingPolicies[policyID]
		}

		cam := defs.Camera{
			ID:                row.ID,
			Name:              row.Name,
			SourceType:        defs.CameraSourceType(row.SourceType),
			SourceURL:         row.SourceURL,
			RecordingPolicyID: &policyID,
		}
		p, err := defs.PathFromCamera(cam)
		if err != nil {
			return added, fmt.Errorf("camera %s: %w", row.Name, err)
		}
		if policyCfg != nil {
			defs.ApplyPolicyToPath(p, defs.RecordingPolicyFromConfig(policyID, policyCfg))
		}
		p.RecordingPolicyID = policyID

		op, err := optionalPathFromConfPath(p)
		if err != nil {
			return added, fmt.Errorf("camera %s: %w", row.Name, err)
		}
		if err := cnf.AddPath(row.Name, op); err != nil {
			return added, fmt.Errorf("camera %s: %w", row.Name, err)
		}
		added++
	}

	if added == 0 {
		return 0, nil
	}
	if err := cnf.Validate(nil); err != nil {
		return added, fmt.Errorf("validate reconciled conf: %w", err)
	}
	// Validate rebuilds Paths; re-stamp the store-derived IDs the same
	// way the create flow does (deterministic from the name).
	for _, row := range rows {
		if p, ok := cnf.Paths[row.Name]; ok && p.ID == "" {
			p.ID = row.ID
		}
	}
	return added, nil
}
