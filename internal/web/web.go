// Package web embeds the recorder configuration SPA into the
// recorder binary at compile time and serves it from the API
// server's HTTP router.
//
// The SPA source lives at the workspace root in `web/` and builds
// to `web/dist/`. The pre-built bundle is copied into `dist/` here
// (this package directory) so `go:embed` picks it up at compile
// time. Refresh the embedded bundle by running `make web` from the
// recorder root.
//
// Build artifacts in `dist/` are tracked in git: the recorder is a
// Go binary first, the SPA bundle is one of its inputs, and a
// committed bundle keeps `go build` self-contained — no separate
// npm install / npm build needed for downstream consumers cloning
// the recorder repo. The trade-off is that web/ source changes
// require running `make web` to refresh the embed and committing
// the resulting dist diff alongside the source diff. The size cost
// (~270 kB JS + ~3 kB CSS in the recorder binary) is negligible
// against the recorder's existing footprint.
package web

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/gin-gonic/gin"
)

// distFS embeds the SPA bundle. The all: prefix tells go:embed to
// include files starting with "_" or "." too, defensively (Vite
// doesn't currently emit any but a future plugin might).
//
//go:embed all:dist
var distFS embed.FS

// distRoot returns an fs.FS rooted at the embedded dist directory,
// so a request for `/index.html` resolves to `dist/index.html`
// without the caller having to know about the prefix.
func distRoot() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}

// Register installs the SPA static handlers onto the supplied gin
// router. Every non-/v1 GET path resolves to the SPA: known asset
// paths return their bytes with the appropriate Content-Type;
// unknown paths return index.html so the client-side hash router
// can take over (matches the SPA's hash-routing model — a deep
// link to /#/cameras is just a hash on /).
//
// Caller invariant: this must be registered AFTER the /v1 group's
// routes so the API surface takes precedence over the SPA's
// catch-all. Gin's NoRoute mechanism is what we use; the API's
// existing GET handlers will match before NoRoute fires.
func Register(router *gin.Engine) error {
	root, err := distRoot()
	if err != nil {
		return err
	}

	// Static asset directory. Vite outputs hashed filenames under
	// /assets/, so a long cache header is safe (the hash invalidates
	// the URL whenever the bundle changes).
	router.GET("/assets/*filepath", func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		serveFS(c, root, "assets"+c.Param("filepath"))
	})

	// Index — both the bare `/` and any unknown GET path. The HTML
	// is short-cache so the SPA picks up new asset hashes after a
	// `make web` redeploy.
	indexHandler := func(c *gin.Context) {
		c.Header("Cache-Control", "no-cache")
		serveFS(c, root, "index.html")
	}
	router.GET("/", indexHandler)
	router.NoRoute(func(c *gin.Context) {
		// The recorder's /v1 API is precedence-handled by gin's
		// route table; NoRoute fires for paths that didn't match
		// any registered handler. Forward GETs to index.html so
		// hash-routing deep links work; everything else gets 404.
		if c.Request.Method == http.MethodGet {
			indexHandler(c)
			return
		}
		c.Status(http.StatusNotFound)
	})

	return nil
}

// serveFS reads `path` out of the embedded fs and writes it back to
// the caller with a content-type guess from the file extension.
func serveFS(c *gin.Context, root fs.FS, path string) {
	data, err := fs.ReadFile(root, path)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.Data(http.StatusOK, contentType(path), data)
}

// contentType returns a MIME type for a path. Covers the file types
// Vite actually emits; falls back to application/octet-stream so an
// unexpected emit never crashes the server.
func contentType(path string) string {
	switch {
	case has(path, ".html"):
		return "text/html; charset=utf-8"
	case has(path, ".js"):
		return "application/javascript; charset=utf-8"
	case has(path, ".css"):
		return "text/css; charset=utf-8"
	case has(path, ".svg"):
		return "image/svg+xml"
	case has(path, ".png"):
		return "image/png"
	case has(path, ".jpg"), has(path, ".jpeg"):
		return "image/jpeg"
	case has(path, ".woff2"):
		return "font/woff2"
	case has(path, ".woff"):
		return "font/woff"
	case has(path, ".ico"):
		return "image/x-icon"
	case has(path, ".json"):
		return "application/json"
	}
	return "application/octet-stream"
}

func has(s, suffix string) bool {
	if len(s) < len(suffix) {
		return false
	}
	return s[len(s)-len(suffix):] == suffix
}
