# Plan: lms-mvp

## Goal

Build the MVP of an independent, production-ready, publicly self-service **multi-tenant SaaS** online learning platform (Tasks 1-6 of `plan.md`'s roadmap): any customer can sign up and create their own organization from launch, a teacher in that organization can author and publish a course, a learner can register, pay, learn, submit work, and earn a certificate — with every organization's data fully isolated from every other's. Everything beyond Tasks 1-6 (communities, AI tools, SCORM, podcasts, the full admin console with feature flags/quotas/CLI/backups, mobile, full hardening) is explicitly out of scope for this plan — Task 6 does include a minimal read-only admin dashboard (org-scoped + platform-owner cross-org view), distinct from that later full admin console.

## Approach

Go (Gin) backend that both server-renders HTML (HTMX + Tailwind) and exposes a versioned JSON API from day one, backed by Supabase Postgres with Row-Level Security as the tenant-isolation boundary, Supabase Auth as the identity source of truth, bunny.net for video, Supabase Storage for other files, Redis-backed background jobs, Resend for email, and Razorpay for payments. Deployed as Docker Compose services on a single VPS for staging/production. Work proceeds in the same dependency order as `plan.md`'s own Task 1-6 breakdown, since each task's schema and services are genuine prerequisites for the next (infra before auth, auth/tenancy before course domain, course domain before both the learner journey and commerce, which can then proceed in parallel).

## Decisions & Rejected Alternatives

- **MVP boundary is exactly Tasks 1-6** — matches `plan.md`'s own "Recommended MVP boundary" section. Rejected: trimming further (payments/certs deferred) or expanding early (search/notifications pulled in) — the existing boundary was judged right-sized.
- **Public multi-tenant SaaS from MVP launch, not single-org-first** — any authenticated user can self-service-create an organization from day one; RLS-based isolation (already planned) makes this safe without additional schema work. Supersedes an earlier draft of Task 1 that framed the product as self-hosted software and `plan.md`'s "one organization per installation initially" phrasing. Rejected: gating org creation behind platform-owner approval, or deferring public signup to post-MVP — the platform owner explicitly wants public SaaS signup at launch.
- **License: Proprietary / All Rights Reserved** — since this is operated SaaS, not distributed software, customers never receive a copy of the code, so there's no OSS obligation. Rejected: MIT (no reason to open-source), AGPLv3 (copyleft protection is moot when nobody receives the code).
- **Platform monetization: commission on course sales, computed in Task 6** — not a separate organization subscription/billing system. Rejected: charging organizations a recurring platform fee (would need a whole new billing flow not built by any task); leaving monetization undefined for MVP (the product brief had drifted into implying org billing without any task building it).
- **Basic admin dashboard is built in Task 6**, not a separate task and not deferred — org-scoped (members, courses, enrollment/revenue) plus a platform-owner cross-org view, read-only, no bulk actions/feature flags/quotas. Rejected: a dedicated new task (adds a phase to the critical path for a small deliverable); dropping it from MVP entirely (Task 1's brief and `plan.md`'s own MVP boundary both call for a basic admin page, and it had no owning task before this fix).
- **Product name/domain deferred as a placeholder** — naming/branding (Task 1) must not block engineering start. Rejected: picking a name now — no name was ready and it isn't a technical dependency.
- **Go web framework: Gin** — widest adoption/middleware ecosystem/docs for the stack's chosen language. Rejected: Fiber (fasthttp, less net/http middleware compatibility), stdlib net/http + router (more plumbing to hand-roll).
- **Backend serves both server-rendered HTML and a versioned JSON API from day one** — avoids a rewrite when Task 12's mobile work starts. Rejected: HTML-only now, API designed later — would risk retrofitting auth/serialization concerns into existing handlers.
- **Supabase Auth is the identity source of truth**; Go verifies its JWTs and layers org/role/permission logic on top. Rejected: fully custom Go auth system — more control but re-implements security-critical, already-solved primitives (password hashing, verification, sessions).
- **Tenant isolation via Postgres RLS + `org_id` on every organization-owned table**, as defense-in-depth alongside Go-layer checks. Rejected: application-layer-only filtering — one missed `WHERE org_id = ...` becomes a silent cross-tenant leak with no DB backstop.
- **Go connects directly to Supabase Postgres (pgx) and issues `SET LOCAL` session variables per request/transaction** so RLS policies can read `current_setting()`. Rejected: routing all queries through Supabase's PostgREST/data API — too constraining for Go-side business logic and complex joins.
- **Migrations: golang-migrate** — plain SQL, framework-agnostic, works directly against the Supabase connection string. Rejected: Supabase CLI migrations (couples schema management to the Supabase CLI/dashboard), goose (equivalent but less-adopted alternative).
- **Payments: Razorpay** as the sole MVP provider, behind the adapter interface `plan.md` Task 6 already calls for. Rejected: Stripe as primary — Razorpay fits an India-focused market; Stripe can be added later as a second adapter.
- **Media storage split: bunny.net for video (streaming/CDN), Supabase Storage for everything else** (thumbnails, certificate PDFs, assignment submissions). Rejected: Supabase Storage for all media (no CDN/streaming optimization for video), bunny.net for all files (unnecessary for small non-video assets already covered by Storage + RLS).
- **Redis-backed job queue (e.g. asynq) for async work** — email sending, webhook processing, media callbacks — keeps request handlers fast and webhook retries reliable. Rejected: skipping Redis for MVP and handling these synchronously.
- **Email: Resend** for transactional email. Rejected: Postmark (more enterprise-oriented than needed yet), AWS SES (more setup overhead — DKIM, sandbox limits — before it can send freely).
- **Hosting: single VPS via Docker Compose** for staging and production. Rejected: managed container PaaS (Fly.io/Railway/Render) — higher cost/less control than needed pre-scale; deferring the decision to Task 12 — the plan needs a concrete target now to shape Task 2's deployment config.
- **Auth scope for MVP: email/password only**; Google OAuth and MFA deferred past MVP. Matches `plan.md`'s own sequencing ("OAuth only after password flow is stable") and MVP boundary ("Email authentication"). Rejected: including Google OAuth in MVP.
- **Block editor MVP scope: text, image, video, file, quiz blocks only** — matches the MVP boundary's explicit list ("Video, text, files, and quizzes"). Rejected: building the full block set (audio, embeds, callouts, code, downloads-as-distinct-type) up front — deferred to post-MVP incremental additions.
- **Certificate PDFs generated server-side in Go** (e.g. chromedp/HTML-to-PDF or a Go PDF library) rendering a branded template. Rejected: a third-party PDF/certificate API (DocRaptor, PDFShift) — adds a vendor and per-certificate cost not needed yet.
- **Tests written alongside MVP work for security/money-critical paths** — auth, tenant isolation, and payment/webhook logic get tests as Tasks 3 and 6 are built, rather than deferring all testing to Task 11 as the phase ordering might literally suggest. Broader test-suite breadth (accessibility, load tests) stays in Task 11.

## Tasks

| # | Task | Phase | Depends on | Status |
|---|------|-------|------------|--------|
| 1 | Product identity, repository, and requirements | 1 | — | pending |
| 2 | Local infrastructure, configuration, and deployment foundation | 2 | 1 | pending |
| 3 | Authentication, organizations, tenancy, and permissions | 3 | 2 | pending |
| 4 | Course domain, media, authoring, and publishing | 4 | 3 | pending |
| 5 | Learner portal, player, progress, assessments, and certificates | 5 | 4 | pending |
| 6 | Catalog, offers, checkout, payments, entitlements, and admin dashboard | 5 | 3, 4 | pending |
| 7 | Human-readable plan overview | 6 | 1, 2, 3, 4, 5, 6 | pending |

## Execution phases

- **Phase 1 (parallel):** task-1
- **Phase 2 (parallel):** task-2
- **Phase 3 (parallel):** task-3
- **Phase 4 (parallel):** task-4
- **Phase 5 (parallel):** task-5, task-6
- **Phase 6 (parallel):** task-7
