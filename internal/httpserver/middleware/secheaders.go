package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"

	"growth-lms/internal/config"
)

// embedPathPrefix is the one route family meant to be framed by third-party
// sites (the embeddable course catalog, see handlers.EmbedCatalog). Requests
// under it opt out of the frame-blocking headers below so external iframes
// keep working; everything else is denied framing to prevent clickjacking.
const embedPathPrefix = "/embed/"

// razorpayOrigin is the wildcard for the hosted Razorpay checkout widget,
// which cannot be self-hosted. Razorpay's checkout.js loads helper scripts and
// makes API/telemetry calls across several *.razorpay.com subdomains, so both
// script-src and connect-src must allow the wildcard or checkout breaks. htmx
// is served from our own origin (see the static package), so 'self' covers it.
const razorpayOrigin = "https://*.razorpay.com"

// appCSP is the policy for the app's own HTML pages. script-src is restricted
// to first-party (plus Razorpay), which blocks an injected <script src> from
// loading attacker-hosted code; connect-src is likewise restricted so injected
// script cannot exfiltrate to an arbitrary origin.
//
// 'unsafe-inline' is still present: the server-rendered templates carry inline
// on* handlers and inline <script> blocks. Removing it requires refactoring
// those to nonce'd/external scripts across every template — a scoped follow-up
// (see docs/operations/security-hardening.md) — so for now the origin
// restriction is the meaningful tightening. User-authored HTML remains
// defended by the sanitize package's allowlist.
//
// frame-src / media-src / img-src stay at https: so the Bunny video iframe and
// CDN-served media keep working without enumerating every media host.
const appCSP = "default-src 'self'; " +
	"script-src 'self' 'unsafe-inline' " + razorpayOrigin + "; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: https:; " +
	"media-src 'self' https:; " +
	"connect-src 'self' " + razorpayOrigin + "; " +
	"frame-src 'self' https:; " +
	"frame-ancestors 'none'; " +
	"object-src 'none'; " +
	"base-uri 'self'"

// embedCSP is the relaxed policy for the embeddable catalog under /embed/,
// which is meant to be framed by third-party sites — so it drops the
// frame-ancestors/X-Frame-Options lockdown while keeping the other limits.
const embedCSP = "default-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: https:; " +
	"object-src 'none'; " +
	"base-uri 'self'"

// SecurityHeaders sets a baseline of hardening response headers on every route
// (plan.md Task 11 release gate: "Secure cookies, CSRF, CORS, trusted origins
// ... XSS ..."). Gin's engine sends none of these by default, so it is mounted
// globally near the top of the chain.
func SecurityHeaders(cfg *config.Config) gin.HandlerFunc {
	// HSTS is only meaningful (and only safe) where the app is served over
	// TLS. Emitting it in development would pin http->https on localhost.
	sendHSTS := cfg.Env != config.EnvDevelopment

	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

		if sendHSTS {
			// 2 years, subdomains included — matches the launch checklist's
			// TLS-everywhere posture (Task 12).
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}

		if strings.HasPrefix(c.Request.URL.Path, embedPathPrefix) {
			h.Set("Content-Security-Policy", embedCSP)
		} else {
			h.Set("X-Frame-Options", "DENY")
			h.Set("Content-Security-Policy", appCSP)
		}

		c.Next()
	}
}
