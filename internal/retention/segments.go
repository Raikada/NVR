package retention

import (
	"context"
	"time"

	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/store"
)

// SegmentLister enumerates and removes recording segments older than the
// per-camera retention horizon. Phase 6 supplies the concrete adapter
// against internal/recordstore; tests pass a no-op or in-memory impl.
type SegmentLister interface {
	// SweepCamera removes segments older than horizon for the given
	// camera. Returns the number of segments removed (best-effort).
	SweepCamera(ctx context.Context, cameraID string, horizon time.Time) (int, error)
}

// SegmentsSweeper iterates every camera, looks up its policy retention,
// and asks the SegmentLister to remove segments older than `now-retention`.
type SegmentsSweeper struct {
	cameras  *store.CamerasRepo
	policies *store.RecordingPoliciesRepo
	lister   SegmentLister
	interval time.Duration
	logger   logger.Writer
}

// NewSegmentsSweeper builds a SegmentsSweeper. interval defaults to 30m.
// lister may be nil in tests; in that case Sweep is a no-op.
func NewSegmentsSweeper(
	cameras *store.CamerasRepo,
	policies *store.RecordingPoliciesRepo,
	lister SegmentLister,
	interval time.Duration,
	log logger.Writer,
) *SegmentsSweeper {
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	return &SegmentsSweeper{cameras, policies, lister, interval, log}
}

// Name implements Sweeper.
func (s *SegmentsSweeper) Name() string { return "segments" }

// Interval implements Sweeper.
func (s *SegmentsSweeper) Interval() time.Duration { return s.interval }

// Sweep visits every enabled camera and asks the lister to remove old
// segments. Per-camera errors are logged but do not abort the loop.
func (s *SegmentsSweeper) Sweep(ctx context.Context) error {
	if s.lister == nil {
		return nil
	}
	cams, err := s.cameras.List(ctx, store.ListCamerasFilter{Limit: 500})
	if err != nil {
		return err
	}
	for _, cam := range cams {
		if !cam.Enabled {
			continue
		}
		policyID := cam.RecordingPolicyID
		if policyID == "" {
			policyID = "policy_default"
		}
		pol, err := s.policies.GetByID(ctx, policyID)
		if err != nil {
			if s.logger != nil {
				s.logger.Log(logger.Warn, "[retention.segments] policy %s for cam %s: %v", policyID, cam.ID, err)
			}
			continue
		}
		retention := time.Duration(pol.RetentionDurationSeconds) * time.Second
		horizon := time.Now().UTC().Add(-retention)
		removed, err := s.lister.SweepCamera(ctx, cam.ID, horizon)
		if err != nil && s.logger != nil {
			s.logger.Log(logger.Warn, "[retention.segments] sweep cam %s: %v", cam.ID, err)
		}
		if removed > 0 && s.logger != nil {
			s.logger.Log(logger.Debug, "[retention.segments] cam %s: removed %d segments", cam.ID, removed)
		}
	}
	return nil
}
