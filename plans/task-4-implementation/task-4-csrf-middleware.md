---
task: 4
name: csrf-middleware
parallel_group: 1
depends_on: []
issue: N/A (disk-only draft, no GitHub remote configured)
---

# Task 4: Double-submit-cookie CSRF middleware

## What to build

New file `internal/httpserver/middleware/csrf.go`, no new dependency
(stdlib `crypto/rand` + `encoding/base64` for token generation,
`crypto/subtle.ConstantTimeCompare` for comparison).

- `CSRFToken(c *gin.Context) string` — reads or lazily issues a random
  token stored in a non-HttpOnly cookie named `lms_csrf` (so page-embedded
  JS/htmx config can read it), scoped `SameSite=Lax`, `Secure` in
  non-development envs (mirror `middleware.SessionCookieName`'s cookie
  attribute choices, check `internal/httpserver/handlers/auth.go` for how
  the session cookie is set for consistency).
- `RequireCSRF() gin.HandlerFunc` — for state-changing requests (POST,
  PATCH, PUT, DELETE), compares the `lms_csrf` cookie value against an
  `X-CSRF-Token` request header using constant-time comparison; aborts
  401/403 on mismatch or either being empty. GET/HEAD/OPTIONS pass through
  untouched (need the cookie-issuing side to still run so pages can read
  the token to send back on the next mutation).
- This middleware is applied ONLY to the new HTML/HTMX course-editor route
  group added in Task 11/10 — JSON API routes (bearer-token auth, no
  ambient cookie) stay exempt, matching Task 3's grilling record Q57/Q58
  intent. Do not apply it to `/api/*` JSON routes.

## Acceptance criteria

- [ ] A GET to an HTML course-editor route issues/refreshes the `lms_csrf`
      cookie and the page can read its value (non-HttpOnly).
- [ ] A POST/PATCH/DELETE to an HTML course-editor route without a
      matching `X-CSRF-Token` header is rejected.
- [ ] A POST/PATCH/DELETE with a correct matching header succeeds (assuming
      it's otherwise valid).
- [ ] JSON API routes under `/api/*` are entirely unaffected — no CSRF
      check applied there.
- [ ] Unit test covering: missing token, mismatched token, matching token.

## Commit convention

This is a disk-only plan (no GitHub remote configured) — commit normally
with a descriptive message; no `Closes #` trailer applies.
