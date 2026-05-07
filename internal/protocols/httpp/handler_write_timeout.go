package httpp

import (
	"net/http"
	"time"
)

type writeTimeoutWriter struct {
	w       http.ResponseWriter
	rc      *http.ResponseController
	timeout time.Duration
}

func (w *writeTimeoutWriter) Header() http.Header {
	return w.w.Header()
}

func (w *writeTimeoutWriter) Write(p []byte) (int, error) {
	w.rc.SetWriteDeadline(time.Now().Add(w.timeout)) //nolint:errcheck
	return w.w.Write(p)
}

func (w *writeTimeoutWriter) WriteHeader(statusCode int) {
	w.rc.SetWriteDeadline(time.Now().Add(w.timeout)) //nolint:errcheck
	w.w.WriteHeader(statusCode)
}

// Flush forwards to the underlying writer's Flush method when present.
// SSE streams (e.g. /v1/events/stream) rely on Flush so the client
// receives data eagerly rather than after the entire response body
// completes. Without this passthrough, gin's responseWriter type-asserts
// for http.Flusher, fails, and silently drops the flush — the client
// then waits forever for data that's still buffered server-side.
func (w *writeTimeoutWriter) Flush() {
	if f, ok := w.w.(http.Flusher); ok {
		w.rc.SetWriteDeadline(time.Now().Add(w.timeout)) //nolint:errcheck
		f.Flush()
	}
}

// apply write deadline before every Write() call.
// this allows to write long responses, splitted in chunks,
// without causing timeouts.
type handlerWriteTimeout struct {
	h       http.Handler
	timeout time.Duration
}

func (h *handlerWriteTimeout) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ww := &writeTimeoutWriter{
		w:       w,
		rc:      http.NewResponseController(w),
		timeout: h.timeout,
	}

	h.h.ServeHTTP(ww, r)
}
