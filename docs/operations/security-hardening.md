# Security Hardening (Task 11)

Reference for the security controls added in the Task 11 release gate and the
one intentional follow-up. Companion to [secret-management.md](secret-management.md).

## HTTP response headers

Set globally by `middleware.SecurityHeaders` (mounted in `server.go` after
CORS). Every response carries:

| Header | Value | Purpose |
| --- | --- | --- |
| `X-Content-Type-Options` | `nosniff` | Stop MIME sniffing |
| `Referrer-Policy` | `strict-origin-when-cross-origin` | Limit referrer leakage |
| `Permissions-Policy` | `camera=(), microphone=(), geolocation=()` | Deny powerful features |
| `Strict-Transport-Security` | `max-age=63072000; includeSubDomains` | Force HTTPS (non-dev only) |
| `X-Frame-Options` | `DENY` | Clickjacking (app pages) |
| `Content-Security-Policy` | see below | XSS / injection defense-in-depth |

HSTS is emitted only outside development (it must not pin `http→https` on
localhost).

## Content-Security-Policy

Two policies, chosen per request path:

- **App pages** (default): `default-src 'self'` with `script-src`/`connect-src`
  limited to first-party plus `https://*.razorpay.com` (the hosted checkout
  widget), `object-src 'none'`, `base-uri 'self'`, `frame-ancestors 'none'`.
- **Embeddable catalog** (`/embed/*`): same origin limits but WITHOUT the
  frame-blocking directives, because that surface is meant to be iframed by
  third-party sites.

### htmx is self-hosted

htmx is vendored at `internal/httpserver/static/htmx.min.js` and served from
`/static/htmx.min.js` (see the `static` package) instead of a CDN. This removes
a supply-chain risk (a compromised CDN could inject script into every page) and
is what lets `script-src` stay `'self'` rather than allowlisting `unpkg.com`.
To upgrade htmx, re-download the pinned version into that file and bump
`htmxVersion`.

### Known follow-up: remove `'unsafe-inline'` from `script-src`

The CSP still allows `'unsafe-inline'` for scripts. The server-rendered
templates use ~22 inline `on*` handlers (`onclick`, `onsubmit`, …) and inline
`<script>` blocks; dropping `'unsafe-inline'` would break them all. Doing so
correctly requires refactoring every inline handler to an addEventListener in a
nonce'd (or external, self-hosted) script, threaded through each template's
render. That is a scoped frontend project, deferred deliberately.

Until then, the meaningful protection in place is the **origin restriction**:
an injected `<script src>` cannot load attacker-hosted code, and injected
script cannot `connect`/exfiltrate to an arbitrary origin. User-authored HTML
is independently defended by the `sanitize` package's allowlist.

## Upload validation

`internal/media/upload_policy.go`, enforced in the media handlers:

- **Filename sanitization** — rejects path separators, `..` traversal, control
  characters; only a safe base name is stored.
- **Extension allowlist** per asset type (image / video / file). SVG is
  excluded from images (it can carry script).
- **Archive rejection** — `.zip/.tar/.gz/...` are refused on every type; SCORM
  packages use their own dedicated importer, not the media path.
- **Size ceilings** — enforced server-side at upload completion via the storage
  `HeadObject` byte count (image 10 MiB, file 100 MiB, video 5 GiB); oversize
  uploads are marked failed and rejected with 413.

## CI security gates

`.github/workflows/ci.yml`:

- **gitleaks** — secret scanning on every push.
- **govulncheck** — dependency CVE scanning, reachability-aware; the Go
  toolchain is pinned to a concrete patch so the gate is deterministic.
- **Trivy** — container image scan of the production image; fails on
  fixable `HIGH`/`CRITICAL`.

## Load / performance

- k6 SLO load tests for catalog / player / checkout / webhook live in
  `loadtest/` (thresholds fail the run on breach). See `loadtest/README.md`.
- The anonymous published-course catalog is served through a Redis read-through
  cache (`internal/cache`), invalidated on course publish/unpublish.

## Accessibility

Server-rendered templates carry: a skip-to-content link and `<nav aria-label>`
landmark (shared `nav.html`), a `<main id="main-content">` landmark per page,
labels/`aria-label` on form fields, `alt` on images, a captions `<track>` on
the lesson-player video, and a shared `:focus-visible` outline.
