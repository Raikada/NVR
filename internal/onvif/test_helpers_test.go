package onvif

import (
	"context"
	"testing"
)

// testContext returns a context bound to t.Cleanup so per-test
// goroutines tear down at test exit without leaking.
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

// contextWithCancel is a tiny shim so tests don't need to import
// context.
func contextWithCancel() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}
