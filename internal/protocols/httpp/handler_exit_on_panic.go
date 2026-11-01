package httpp

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
)

// exit when there's a panic inside the HTTP handler.
// https://github.com/golang/go/issues/16542
type handlerExitOnPanic struct {
	h http.Handler
}

func (h *handlerExitOnPanic) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		err := recover()
		if err == nil {
			return
		}

		// http.ErrAbortHandler is not a crash: it's the sentinel panic
		// the stdlib raises to abort a response — httputil.ReverseProxy
		// raises it whenever a client disconnects mid-stream, which is
		// constant for the /hls and /playback media proxies (the
		// console's MSE playback aborts window fetches on every seek and
		// re-chunk). net/http's own server recovers ErrAbortHandler
		// silently; this wrapper sits inside that recover, so catching
		// it here and calling os.Exit turned every aborted proxied media
		// fetch into a full recorder restart (crash loop). Re-raise it so
		// it propagates to net/http's server, which aborts the response
		// without logging — exactly as if this wrapper weren't here.
		if err == http.ErrAbortHandler {
			panic(err)
		}

		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		fmt.Fprintf(os.Stderr, "panic: %v\n\n%s", err, buf[:n])
		os.Exit(1)
	}()
	h.h.ServeHTTP(w, r)
}
