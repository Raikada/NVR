package outbox

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestProcessor_DispatchesAndMarks(t *testing.T) {
	var (
		mu         sync.Mutex
		dispatched []string
		delivered  []string
		failed     []string
		dead       []string
	)
	queued := []*Job{
		{ID: "a", PayloadJSON: "{}", Attempts: 0, Extra: ResultDelivered},
		{ID: "b", PayloadJSON: "{}", Attempts: 0, Extra: ResultRetry},
		{ID: "c", PayloadJSON: "{}", Attempts: 7, Extra: ResultRetry}, // hits MaxAttempts
		{ID: "d", PayloadJSON: "{}", Attempts: 0, Extra: ResultDead},
	}
	served := false
	funcs := Funcs{
		Claim: func(_ context.Context, _ int) ([]*Job, error) {
			mu.Lock()
			defer mu.Unlock()
			if served {
				return nil, nil
			}
			served = true
			return queued, nil
		},
		Dispatch: func(_ context.Context, j *Job) (Result, string, error) {
			mu.Lock()
			dispatched = append(dispatched, j.ID)
			mu.Unlock()
			return j.Extra.(Result), "synthetic", nil
		},
		MarkDelivered: func(_ context.Context, id string) error {
			mu.Lock()
			delivered = append(delivered, id)
			mu.Unlock()
			return nil
		},
		MarkFailed: func(_ context.Context, id, _ string, _ time.Time) error {
			mu.Lock()
			failed = append(failed, id)
			mu.Unlock()
			return nil
		},
		MarkDead: func(_ context.Context, id, _ string) error {
			mu.Lock()
			dead = append(dead, id)
			mu.Unlock()
			return nil
		},
	}
	cfg := Config{Workers: 1, PollInterval: 5 * time.Millisecond, MaxAttempts: 8}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	NewProcessor(funcs, cfg, nil).Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if len(delivered) != 1 || delivered[0] != "a" {
		t.Errorf("delivered: got %v", delivered)
	}
	if len(failed) != 1 || failed[0] != "b" {
		t.Errorf("failed: got %v", failed)
	}
	if len(dead) != 2 || !contains(dead, "c") || !contains(dead, "d") {
		t.Errorf("dead: got %v", dead)
	}
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
