// Package outbox is a generic "claim -> dispatch -> mark" processor used
// by both internal/notifications (webhook + SMTP delivery) and
// internal/cloudbridge (events/health/audit pushed to a future cloud
// product). The package is intentionally agnostic to row shape; callers
// supply Claim, Dispatch, MarkDelivered, MarkFailed, MarkDead funcs.
package outbox

import (
	"math/rand"
	"time"
)

// DefaultBackoffSteps is the canonical exponential backoff schedule
// (jittered ±25% per attempt). Index 0 is used after attempt 1, index 1
// after attempt 2, etc. Total cap ~ 4h22m across 8 attempts.
var DefaultBackoffSteps = []time.Duration{
	30 * time.Second,
	1 * time.Minute,
	2 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	30 * time.Minute,
	1 * time.Hour,
	2 * time.Hour,
}

// NextAttemptDelay returns the duration to wait before retry given the
// number of attempts already made. Adds ±25% jitter. attempts is 1-based
// (1 == first delay).
func NextAttemptDelay(attempts int, steps []time.Duration, rng *rand.Rand) time.Duration {
	if len(steps) == 0 {
		steps = DefaultBackoffSteps
	}
	idx := attempts - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(steps) {
		idx = len(steps) - 1
	}
	base := steps[idx]
	jitter := time.Duration(float64(base) * 0.25 * (rng.Float64()*2 - 1))
	return base + jitter
}
