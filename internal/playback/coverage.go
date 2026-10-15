// Package playback: exported coverage helper (Phase A B4). The /v1
// API wraps this so timeline clients get merged {start,duration}
// footage ranges with true durations parsed from the fmp4 parts —
// without talking to the playback port directly.
package playback

import (
	"errors"
	"io/fs"
	"time"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/recordstore"
)

// CoverageEntry is one contiguous recorded range.
type CoverageEntry struct {
	Start    time.Time `json:"start"`
	Duration float64   `json:"duration"` // seconds
}

// Coverage lists merged recorded ranges for a path, optionally bounded
// by from/to (zero values = unbounded). Returns an empty slice when no
// segments exist in the window.
func Coverage(pathConf *conf.Path, pathName string, from, to time.Time) ([]CoverageEntry, error) {
	var start, end *time.Time
	if !from.IsZero() {
		start = &from
	}
	if !to.IsZero() {
		end = &to
	}
	segments, err := recordstore.FindSegments(pathConf, pathName, start, end)
	if err != nil {
		// Not-exist covers cameras that have never recorded (no
		// per-path directory yet) — that's "no footage", not an error.
		if err == recordstore.ErrNoSegmentsFound || errors.Is(err, fs.ErrNotExist) {
			return []CoverageEntry{}, nil
		}
		return nil, err
	}
	entries, err := parseAndConcatenate(pathConf.RecordFormat, segments)
	if err != nil {
		return nil, err
	}
	out := make([]CoverageEntry, 0, len(entries))
	for _, e := range entries {
		end := e.Start.Add(time.Duration(e.Duration))
		// FindSegments keeps the segment that may contain `from`
		// (playback-seek semantics); coverage wants strict windowing.
		if !from.IsZero() && !end.After(from) {
			continue
		}
		if !to.IsZero() && !e.Start.Before(to) {
			continue
		}
		out = append(out, CoverageEntry{
			Start:    e.Start,
			Duration: time.Duration(e.Duration).Seconds(),
		})
	}
	return out, nil
}
