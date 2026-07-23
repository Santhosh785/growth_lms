// Package static embeds and serves the app's first-party static assets and PWA
// files.
//
// htmx is vendored here (rather than loaded from a CDN) so the frontend has no
// third-party script dependency at runtime: it removes a supply-chain risk
// (a compromised CDN could inject script into every page) and lets the
// Content-Security-Policy restrict script-src to 'self' instead of
// allowlisting an external host. Refresh the vendored file by re-downloading
// the pinned version and updating htmxVersion below.
//
// The PWA files (Task 12) — web app manifest, service worker, icon, offline
// fallback — make the app installable and give it a graceful offline page. The
// service worker (sw.js) is served from the site ROOT (/sw.js) so its control
// scope is the whole origin; browsers scope a worker to its own served path.
package static

import (
	"embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

// htmxVersion is the vendored htmx release. Keep it in sync with htmx.min.js.
const htmxVersion = "1.9.12"

//go:embed htmx.min.js app.js manifest.webmanifest sw.js icon.svg offline.html
var files embed.FS

func mustRead(name string) []byte {
	b, err := files.ReadFile(name)
	if err != nil {
		panic("static: " + err.Error())
	}
	return b
}

var (
	htmxJS   = mustRead("htmx.min.js")
	appJS    = mustRead("app.js")
	manifest = mustRead("manifest.webmanifest")
	swJS     = mustRead("sw.js")
	iconSVG  = mustRead("icon.svg")
	offline  = mustRead("offline.html")
)

// serve returns a handler that writes body with the given content type and
// Cache-Control header.
func serve(contentType, cacheControl string, body []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cacheControl != "" {
			c.Header("Cache-Control", cacheControl)
		}
		c.Data(http.StatusOK, contentType, body)
	}
}

const immutable = "public, max-age=31536000, immutable"

// Register mounts the static asset and PWA routes on engine.
func Register(engine *gin.Engine) {
	// Versioned/immutable first-party assets.
	engine.GET("/static/htmx.min.js", func(c *gin.Context) {
		c.Header("X-Htmx-Version", htmxVersion)
		serve("application/javascript; charset=utf-8", immutable, htmxJS)(c)
	})
	engine.GET("/static/app.js", serve("application/javascript; charset=utf-8", immutable, appJS))
	engine.GET("/static/icon.svg", serve("image/svg+xml", immutable, iconSVG))
	engine.GET("/offline.html", serve("text/html; charset=utf-8", "no-cache", offline))

	// PWA control files. The manifest and service worker are revalidated
	// (no long cache) so an updated worker/manifest is picked up promptly.
	engine.GET("/manifest.webmanifest", serve("application/manifest+json; charset=utf-8", "no-cache", manifest))
	// Served from root so the worker controls the whole origin.
	engine.GET("/sw.js", serve("application/javascript; charset=utf-8", "no-cache", swJS))
}
