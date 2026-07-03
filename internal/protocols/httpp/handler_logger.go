package httpp

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httputil"

	"github.com/bluenviron/mediamtx/internal/logger"
)

type loggerWriter struct {
	w      http.ResponseWriter
	status int
	size   int
}

func (w *loggerWriter) Header() http.Header {
	return w.w.Header()
}

func (w *loggerWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.size += len(b)
	return w.w.Write(b)
}

func (w *loggerWriter) WriteHeader(statusCode int) {
	w.status = statusCode
	w.w.WriteHeader(statusCode)
}

// Flush forwards to the underlying writer's Flush method when present.
// Required for SSE (/v1/events/stream) so chunked-encoded responses
// reach the client as soon as the handler writes; without it,
// gin's responseWriter type-asserts http.Flusher on this wrapper, fails,
// and the client blocks waiting for data still buffered server-side.
func (w *loggerWriter) Flush() {
	if f, ok := w.w.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *loggerWriter) dump() string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s %d %s\n", "HTTP/1.1", w.status, http.StatusText(w.status))
	w.w.Header().Write(&buf) //nolint:errcheck
	buf.Write([]byte("\n"))
	if w.size > 0 {
		fmt.Fprintf(&buf, "(body of %d bytes)", w.size)
	}
	return buf.String()
}

// log requests and responses.
type handlerLogger struct {
	h   http.Handler
	log logger.Writer
}

func (h *handlerLogger) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	byts, _ := httputil.DumpRequest(r, true)
	h.log.Log(logger.Debug, "[conn %v] [c->s] %s", r.RemoteAddr, string(byts))

	logw := &loggerWriter{w: w}

	h.h.ServeHTTP(logw, r)

	h.log.Log(logger.Debug, "[conn %v] [s->c] %s", r.RemoteAddr, logw.dump())
}
