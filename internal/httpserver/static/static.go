// Package static embeds and serves the app's first-party static assets.
//
// htmx is vendored here (rather than loaded from a CDN) so the frontend has no
// third-party script dependency at runtime: it removes a supply-chain risk
// (a compromised CDN could inject script into every page) and lets the
// Content-Security-Policy restrict script-src to 'self' instead of
// allowlisting an external host. Refresh the vendored file by re-downloading
// the pinned version and updating htmxVersion below.
package static

import (
	"embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

// htmxVersion is the vendored htmx release. Keep it in sync with htmx.min.js.
const htmxVersion = "1.9.12"

//go:embed htmx.min.js
var files embed.FS

var htmxJS = mustRead("htmx.min.js")

func mustRead(name string) []byte {
	b, err := files.ReadFile(name)
	if err != nil {
		panic("static: " + err.Error())
	}
	return b
}

// Register mounts the static asset routes on engine. Assets are immutable
// (versioned by content), so they are served with a long-lived cache header.
func Register(engine *gin.Engine) {
	engine.GET("/static/htmx.min.js", func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		c.Header("X-Htmx-Version", htmxVersion)
		c.Data(http.StatusOK, "application/javascript; charset=utf-8", htmxJS)
	})
}
