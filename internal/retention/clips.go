package retention

import (
	"context"
	"os"
	"time"

	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/store"
)

// ClipsSweeper deletes expired ready clips in batches and unlinks the
// underlying files. File-system errors are logged but do not abort the
// row delete (the row is the canonical record of "clip exists").
type ClipsSweeper struct {
	clips     *store.ClipsRepo
	batchSize int
	interval  time.Duration
	logger    logger.Writer
}

// NewClipsSweeper builds a ClipsSweeper. batchSize defaults to 100,
// interval to 15m.
func NewClipsSweeper(clips *store.ClipsRepo, batchSize int, interval time.Duration, log logger.Writer) *ClipsSweeper {
	if batchSize <= 0 {
		batchSize = 100
	}
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	return &ClipsSweeper{clips: clips, batchSize: batchSize, interval: interval, logger: log}
}

// Name implements Sweeper.
func (s *ClipsSweeper) Name() string { return "clips" }

// Interval implements Sweeper.
func (s *ClipsSweeper) Interval() time.Duration { return s.interval }

// Sweep removes expired ready clips and unlinks their output files.
func (s *ClipsSweeper) Sweep(ctx context.Context) error {
	paths, err := s.clips.DeleteExpiredReady(ctx, s.batchSize)
	if err != nil {
		return err
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) && s.logger != nil {
			s.logger.Log(logger.Warn, "[retention.clips] unlink %s: %v", p, err)
		}
	}
	if len(paths) > 0 && s.logger != nil {
		s.logger.Log(logger.Debug, "[retention.clips] removed %d expired ready clips", len(paths))
	}
	return nil
}
