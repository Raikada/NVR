package httpp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// http.ErrAbortHandler must propagate (re-panic), not be swallowed with
// os.Exit. httputil.ReverseProxy raises it whenever a client
// disconnects mid-stream — constant for the /hls and /playback media
// proxies — and treating it as a crash turned every aborted media fetch
// into a full recorder restart. The stdlib server recovers it silently;
// this wrapper must defer to that behaviour.
func TestHandlerExitOnPanicReraisesAbortHandler(t *testing.T) {
	h := &handlerExitOnPanic{h: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	})}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/playback/get", nil)

	require.PanicsWithValue(t, http.ErrAbortHandler, func() {
		h.ServeHTTP(rec, req)
	}, "ErrAbortHandler must propagate so net/http's server aborts the response without crashing the process")
}

// A handler that returns normally must pass through untouched.
func TestHandlerExitOnPanicPassesThrough(t *testing.T) {
	called := false
	h := &handlerExitOnPanic{h: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	require.NotPanics(t, func() { h.ServeHTTP(rec, req) })
	require.True(t, called)
	require.Equal(t, http.StatusNoContent, rec.Code)
}
