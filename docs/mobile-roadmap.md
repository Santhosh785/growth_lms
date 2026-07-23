# Mobile Roadmap

Mobile comes **after** the web product stabilizes (plan.md Task 12). The
sequence is deliberate: reuse everything the web app already proves out before
committing to native app-store builds.

## Stage 0 — Responsive web + PWA (done)

Shipped in Task 12:

- Responsive `<meta viewport>` on every page; fluid, single-column-friendly
  layouts.
- Installable PWA: web app manifest (`/manifest.webmanifest`), icon, and a
  conservative service worker (`/sw.js`) that precaches static assets and shows
  an offline fallback but never caches authenticated pages or API responses.

This gives an "add to home screen" mobile experience with no separate codebase.
Next: audit each page on small screens and tighten any layout that still
assumes desktop width.

## Stage 1 — Harden the REST API as the mobile contract

The mobile app will consume the **same REST API** the web app uses — no
separate backend. Before native work:

- Confirm every learner-facing action has a clean JSON endpoint (not only an
  HTML/HTMX route).
- Document the API (endpoints, auth, error shapes) as the mobile team's
  contract.
- Confirm token-based auth works for a non-browser client (bearer tokens /
  session tokens), independent of cookies/CSRF, and that rate limits suit
  mobile usage.

## Stage 2 — One cross-platform app (Expo / React Native)

- Single Expo/React Native app targeting iOS and Android from one codebase.
- Scope the first version to the **learner** journey: browse catalog, enroll,
  play lessons/video, track progress, view certificates. Authoring stays on
  web initially.
- Payments: use each store's rules for digital goods; keep the
  webhook-grants-access invariant (access only after a verified provider
  webhook, never a client callback).

## Stage 3 — Runtime organization branding

Support per-organization branding **at runtime** (theme, logo, colors fetched
from the org's existing Task 8 branding) *before* investing in separate
per-org app-store builds. One app that re-skins per org beats N near-identical
binaries.

## Explicitly deferred

Native-only features (push notifications, offline course download, deep native
integrations) come only once the cross-platform app has real usage. Do not
build a mobile app before the web MVP and pilot have stabilized.
