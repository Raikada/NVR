// Package api: same-origin media proxy (/hls/*, /playback/*).
//
// The console is served over HTTPS on the API port, but the HLS and
// playback listeners speak plain HTTP on their own ports — browsers
// refuse that combination (TLS failure or mixed content). The API
// reverse-proxies both so the SPA only ever talks to its own origin.
package api //nolint:revive

import (
	ctxpkg "context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
)

func startMediaProxyAPI(t *testing.T) (*http.Client, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	// Fake upstream speaking both the HLS shapes (playlist + the
	// cookieCheck redirect) and the playback /get shape.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/cam1/index.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = w.Write([]byte("#EXTM3U\n"))
		case r.URL.Path == "/redir/index.m3u8" && !strings.Contains(r.URL.RawQuery, "cookieCheck"):
			http.Redirect(w, r, "/redir/index.m3u8?cookieCheck=1", http.StatusFound)
		case r.URL.Path == "/get":
			_, _ = w.Write([]byte("MP4:" + r.URL.RawQuery))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)
	target := strings.TrimPrefix(upstream.URL, "http://")

	la, _ := newTestLocalAuth(t)
	pw, err := la.BootstrapIfEmpty(ctxpkg.Background())
	require.NoError(t, err)
	// Mint the token up-front so the auth manager can be pinned to it —
	// unlike NilAuthManager this lets the anonymous case assert 401.
	res0, err := la.Login(ctxpkg.Background(), "admin", pw, "127.0.0.1")
	require.NoError(t, err)
	token := res0.Token

	cnf := tempConf(t, "api: yes\n")
	a := &API{
		Address: "localhost:9997", ReadTimeout: conf.Duration(10 * time.Second),
		WriteTimeout: conf.Duration(10 * time.Second), Conf: cnf,
		AuthManager: &bearerOnlyAuthManager{token: token}, LocalAuth: la, Parent: &testParent{},
		HLSProxyTarget:      target,
		PlaybackProxyTarget: target,
	}
	require.NoError(t, a.Initialize())
	t.Cleanup(a.Close)
	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	hc := &http.Client{
		Transport: tr,
		// Don't follow redirects — the test asserts Location rewriting.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	return hc, token
}

func TestMediaProxyHLS(t *testing.T) {
	hc, token := startMediaProxyAPI(t)

	req, _ := http.NewRequest(http.MethodGet, "http://localhost:9997/hls/cam1/index.m3u8", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	buf := make([]byte, 64)
	n, _ := res.Body.Read(buf)
	require.Contains(t, string(buf[:n]), "#EXTM3U")
}

// The HLS server's cookieCheck 302 carries a root-relative Location;
// the proxy must re-prefix it so the browser stays on /hls/*.
func TestMediaProxyRewritesRedirect(t *testing.T) {
	hc, token := startMediaProxyAPI(t)

	req, _ := http.NewRequest(http.MethodGet, "http://localhost:9997/hls/redir/index.m3u8", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := hc.Do(req)
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusFound, res.StatusCode)
	loc, err := url.Parse(res.Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "/hls/redir/index.m3u8", loc.Path)
}

// <video> elements and hls.js can't send Authorization headers; they DO
// send the same-origin session cookie. The proxy paths promote it.
func TestMediaProxyAcceptsSessionCookie(t *testing.T) {
	hc, token := startMediaProxyAPI(t)

	req, _ := http.NewRequest(http.MethodGet, "http://localhost:9997/playback/get?path=cam1&start=x", nil)
	req.AddCookie(&http.Cookie{Name: "raikada_session", Value: token})
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	buf := make([]byte, 64)
	n, _ := res.Body.Read(buf)
	require.Equal(t, "MP4:path=cam1&start=x", string(buf[:n]))
}

// Native <video> and the playback element can't send headers; they put
// the token on the query string and the proxy promotes it.
func TestMediaProxyAcceptsQueryToken(t *testing.T) {
	hc, token := startMediaProxyAPI(t)

	req, _ := http.NewRequest(http.MethodGet,
		"http://localhost:9997/playback/get?path=cam1&start=x&access_token="+token, nil)
	res, err := hc.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
}

// The HLS cookieCheck 302 drops the query string; the proxy must carry
// a ?access_token= into the rewritten Location so the native <video>
// path stays authenticated across the redirect.
func TestMediaProxyRedirectCarriesQueryToken(t *testing.T) {
	hc, token := startMediaProxyAPI(t)

	req, _ := http.NewRequest(http.MethodGet,
		"http://localhost:9997/hls/redir/index.m3u8?access_token="+token, nil)
	res, err := hc.Do(req)
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusFound, res.StatusCode)
	loc, err := url.Parse(res.Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "/hls/redir/index.m3u8", loc.Path)
	require.Equal(t, token, loc.Query().Get("access_token"))
}

func TestMediaProxyRequiresAuth(t *testing.T) {
	hc, _ := startMediaProxyAPI(t)

	req, _ := http.NewRequest(http.MethodGet, "http://localhost:9997/hls/cam1/index.m3u8", nil)
	res, err := hc.Do(req)
	require.NoError(t, err)
	res.Body.Close()
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)
}
