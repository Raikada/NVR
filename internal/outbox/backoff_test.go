package outbox

import (
	"math/rand"
	"testing"
	"time"
)

func TestNextAttemptDelay_WithinBounds(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for attempts := 1; attempts <= 8; attempts++ {
		base := DefaultBackoffSteps[attempts-1]
		got := NextAttemptDelay(attempts, nil, rng)
		min, max := time.Duration(float64(base)*0.75), time.Duration(float64(base)*1.25)
		if got < min || got > max {
			t.Errorf("attempt %d: %v not in [%v,%v]", attempts, got, min, max)
		}
	}
}

func TestNextAttemptDelay_CapsAtLastStep(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	got := NextAttemptDelay(20, nil, rng)
	last := DefaultBackoffSteps[len(DefaultBackoffSteps)-1]
	if got > time.Duration(float64(last)*1.25) {
		t.Errorf("expected cap, got %v", got)
	}
}

func TestNextAttemptDelay_ZeroAttemptsNoPanic(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	got := NextAttemptDelay(0, nil, rng)
	if got <= 0 {
		t.Errorf("expected positive delay, got %v", got)
	}
}
