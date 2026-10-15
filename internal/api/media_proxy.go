// Same-origin media proxy. The console is served (typically over
// HTTPS) on the API port, while the HLS and playback listeners speak
// plain HTTP on their own ports. Browsers refuse that combination:
// https://host:8888 fails the TLS handshake and http:// subresources
// are blocked as mixed content. The API therefore reverse-proxies
//
//	/hls/<path...>      → the HLS server      (live streams)
//	/playback/<path...> → the playback server (fmp4 time ranges)
//
// so the SPA only ever talks to its own origin — one port, one TLS
// cert, and the recorder-local JWT (header or session cookie, promoted
// in middlewareAuth) gates the media surface the same as /v1.
package api //nolint:revive

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// newMediaProxy builds a reverse proxy to http://<target> that strips
// the given route prefix on the way in and re-prefixes root-relative
// redirect Locations on the way out (the HLS server's cookieCheck
// flow 302s to "/<path>/index.m3u8?cookieCheck=1").
func newMediaProxy(target, prefix string) *httputil.ReverseProxy {
	upstream := &url.URL{Scheme: "http", Host: target}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	base := proxy.Director
	proxy.Director = func(r *http.Request) {
		base(r)
		r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
		if r.URL.Path == "" {
			r.URL.Path = "/"
		}
		r.Host = upstream.Host
	}
	proxy.ModifyResponse = func(res *http.Response) error {
		if loc := res.Header.Get("Location"); strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, prefix+"/") {
			res.Header.Set("Location", prefix+loc)
		}
		return nil
	}
	return proxy
}

// isMediaProxyPath reports whether the request path rides the media
// proxy. Used by middlewareAuth to promote the session cookie into a
// bearer (video elements can't send Authorization headers).
func isMediaProxyPath(path string) bool {
	return strings.HasPrefix(path, "/hls/") || strings.HasPrefix(path, "/playback/")
}

func (a *API) registerMediaProxies(router gin.IRouter) {
	if a.HLSProxyTarget != "" {
		h := gin.WrapH(newMediaProxy(a.HLSProxyTarget, "/hls"))
		router.GET("/hls/*any", h)
		router.HEAD("/hls/*any", h)
	}
	if a.PlaybackProxyTarget != "" {
		h := gin.WrapH(newMediaProxy(a.PlaybackProxyTarget, "/playback"))
		router.GET("/playback/*any", h)
		router.HEAD("/playback/*any", h)
	}
}
