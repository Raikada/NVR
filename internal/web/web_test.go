package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRegisterServesIndexAtRoot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	require.NoError(t, Register(r))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "text/html")
	require.Contains(t, w.Body.String(), "<div id=\"root\">")
}

func TestRegisterServesAssetsWithImmutableCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	require.NoError(t, Register(r))

	// The SPA bundle puts the logo under /assets/logo.png.
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/assets/logo.png", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "image/png")
	require.Contains(t, w.Header().Get("Cache-Control"), "immutable")
	require.Greater(t, len(w.Body.Bytes()), 0)
}

func TestRegisterFallsBackToIndexForUnknownGet(t *testing.T) {
	// Hash routing means deep links like /#/cameras land on / —
	// but a user might also paste / typed-route paths. Either way,
	// any unknown GET should serve index.html so the SPA can
	// take over.
	gin.SetMode(gin.TestMode)
	r := gin.New()
	require.NoError(t, Register(r))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/cameras", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Header().Get("Content-Type"), "text/html")
}

func TestRegisterReturns404ForUnknownNonGet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	require.NoError(t, Register(r))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/some-unknown-path", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestContentTypeMappings(t *testing.T) {
	cases := map[string]string{
		"x.html":  "text/html",
		"x.js":    "application/javascript",
		"x.css":   "text/css",
		"x.svg":   "image/svg+xml",
		"x.png":   "image/png",
		"x.woff2": "font/woff2",
		"x.bin":   "application/octet-stream",
	}
	for path, want := range cases {
		got := contentType(path)
		require.True(t, strings.HasPrefix(got, want),
			"contentType(%q) = %q, want prefix %q", path, got, want)
	}
}
