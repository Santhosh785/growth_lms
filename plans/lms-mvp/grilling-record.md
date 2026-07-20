# Grilling Record — lms-mvp

> Reference only — not part of the spec. Never read by an executing agent; kept for a future developer or the author revisiting why this plan is shaped the way it is. If this plan is later published to GitHub, this content should move to the parent issue's first comment (see `/draft-plan` Step 6.3) rather than staying in this file.

## Session 1 — initial plan grilling (18 questions)

### Q1: MVP boundary
**Options:** 1. Confirm Tasks 1-6 as-is — 2. Trim further — 3. Expand slightly
**Recommended:** Option 1 (right-sized already)
**Chosen:** Option 1, as recommended

### Q2: Product identity
**Options:** 1. Use a placeholder name for now — 2. Provide the real name/domain now
**Recommended:** Option 1 (don't block engineering on branding)
**Chosen:** Option 1, as recommended

### Q3: Go web framework
**Options:** 1. Gin — 2. Fiber — 3. net/http (stdlib) + router
**Recommended:** Option 1 (Gin — widest ecosystem)
**Chosen:** Option 1, as recommended

### Q4: Frontend architecture
**Options:** 1. HTML templates + JSON API from day one — 2. HTML-only, add JSON API later
**Recommended:** Option 1 (avoids a later rewrite for mobile)
**Chosen:** Option 1, as recommended

### Q5: Auth ownership
**Options:** 1. Supabase Auth as source of truth — 2. Fully custom auth in Go
**Recommended:** Option 1
**Chosen:** Option 1, as recommended

### Q6: Tenant isolation
**Options:** 1. Postgres RLS + org_id on every table — 2. Application-layer checks only
**Recommended:** Option 1 (defense-in-depth)
**Chosen:** Option 1, as recommended

### Q7: DB access pattern
**Options:** 1. Direct Postgres connection + SET LOCAL per request — 2. Go calls Supabase's PostgREST/data API
**Recommended:** Option 1
**Chosen:** Option 1, as recommended

### Q8: Migrations tool
**Options:** 1. golang-migrate — 2. Supabase CLI migrations — 3. goose
**Recommended:** Option 1
**Chosen:** Option 1, as recommended

### Q9: Payment provider
**Options:** 1. Razorpay (India-focused) — 2. Stripe
**Recommended:** Option 1 (conditional on India-focused market)
**Chosen:** Option 1, as recommended

### Q10: Media storage
**Options:** 1. Supabase Storage — 2. S3 / Cloudflare R2
**Recommended:** Option 1
**Chosen:** custom answer: bunny.net (not among presented options) — later refined at Q11

### Q11: Storage split
**Options:** 1. Supabase Storage for non-video, bunny.net for video — 2. bunny.net for all files
**Recommended:** Option 1
**Chosen:** Option 1, as recommended

### Q12: Redis / job queue
**Options:** 1. Redis-backed job queue for async work — 2. Skip Redis for MVP
**Recommended:** Option 1
**Chosen:** Option 1, as recommended

### Q13: Email provider
**Options:** 1. Resend — 2. Postmark — 3. AWS SES
**Recommended:** Option 1
**Chosen:** Option 1, as recommended

### Q14: Hosting target
**Options:** 1. Single VPS via Docker Compose — 2. Managed container platform (Fly.io/Railway/Render) — 3. Not decided yet
**Recommended:** Option 1
**Chosen:** Option 1, as recommended

### Q15: OAuth & MFA
**Options:** 1. Email/password only for MVP, OAuth+MFA after — 2. Include Google OAuth in MVP
**Recommended:** Option 1
**Chosen:** Option 1, as recommended

### Q16: Block editor scope
**Options:** 1. Reduced MVP block set (text, image, video, file, quiz) — 2. Full block set in MVP
**Recommended:** Option 1
**Chosen:** Option 1, as recommended

### Q17: Certificate PDFs
**Options:** 1. Go server-side PDF generation — 2. Third-party PDF/certificate service
**Recommended:** Option 1
**Chosen:** Option 1, as recommended

### Q18: Testing during MVP
**Options:** 1. Test critical paths (auth, tenant isolation, payments) as we build MVP — 2. Strictly defer all testing to Task 11
**Recommended:** Option 1
**Chosen:** Option 1, as recommended

## Session 2 — draft-plan setup (2 questions)

### Q19: Repo setup for publishing
**Options:** 1. Init git + create GitHub repo now — 2. Disk-only draft
**Recommended:** Option 1
**Chosen:** Option 2, against the recommendation: user wanted a disk-only draft first rather than committing to git/GitHub immediately

### Q20: Plan name
**Options:** 1. lms-mvp — 2. growth-lms-foundation
**Recommended:** Option 1
**Chosen:** Option 1, as recommended

## Session 3 — grill-me follow-up on Task 1 (11 questions)

### Q21: DoD numbering bug
**Options:** 1. Fix to match main-plan.md's real task numbers — 2. Leave as-is
**Recommended:** Option 1
**Chosen:** Option 1, as recommended

### Q22: Deployment model
**Options:** 1. Hosted SaaS, single org for now (multi-tenant-ready schema) — 2. Self-hosted software product
**Recommended:** Option 1
**Chosen:** custom answer: "i will porvide this as a saas multi tenant" — reframed the product as SaaS but raised a new question about whether it's multi-org from day one, resolved at Q23

### Q23: MVP tenancy scope
**Options:** 1. Single org live at launch, multi-org-ready architecture — 2. Full public multi-tenant SaaS from MVP launch
**Recommended:** Option 1
**Chosen:** Option 2, against the recommendation: user wants public self-signup and multiple live organizations from MVP launch, not deferred. **This reverses the original plan's Q1/MVP-boundary assumption** ("one organization per installation initially" from `plan.md`, carried into the original Q1 answer) — Task 3's scope was expanded accordingly to include public org self-signup.

### Q24: License choice
**Options:** 1. Proprietary / All Rights Reserved — 2. MIT — 3. AGPLv3
**Recommended:** Option 1 (given confirmed SaaS-not-distributed model from Q22/Q23)
**Chosen:** Option 1, as recommended

### Q25: Payment processor fix
**Options:** 1. Fix Task 1's Stripe references to Razorpay — 2. Leave as Stripe placeholder
**Recommended:** Option 1 (Task 1 had drifted from the Q9 decision)
**Chosen:** Option 1, as recommended

### Q26: Video hosting fix
**Options:** 1. Fix to bunny.net-hosted video — 2. Leave as external-link video
**Recommended:** Option 1 (Task 1 had drifted from the Q10/Q11 decision)
**Chosen:** Option 1, as recommended

### Q27: Role model fix
**Options:** 1. Align Task 1's domain model to the 5-role set (platform_owner, org_owner, teacher, moderator, learner) — 2. Keep 3 roles for MVP, expand later
**Recommended:** Option 1 (Task 1 had drifted from Task 3's already-committed role model)
**Chosen:** Option 1, as recommended

### Q28: Certificate format fix
**Options:** 1. Match Task 5's PDF-only decision — 2. Keep the "PDF or simple HTML" hedge
**Recommended:** Option 1 (Task 1 had drifted from the Q17 decision)
**Chosen:** Option 1, as recommended

### Q29: Security contact domain
**Options:** 1. Keep focasedu.com as the real security contact — 2. Use a generic placeholder instead
**Recommended:** Option 1, conditionally ("if accurate")
**Chosen:** Option 2, against the (conditional) recommendation: user opted for a neutral placeholder rather than committing the real operating domain into the draft before the product's actual domain is decided

### Q30: Deployment target fix
**Options:** 1. Fix Task 1's DoD to reference the VPS/Docker Compose target — 2. Leave Heroku/Railway as an option
**Recommended:** Option 1 (Task 1 had drifted from the Q14 decision)
**Chosen:** Option 1, as recommended

### Q31: Domain model detail level
**Options:** 1. Trim to entities + relationships only — 2. Keep full field-level detail
**Recommended:** Option 1 (avoids Task 1 silently prescribing schema that Tasks 3/4/6 actually own)
**Chosen:** Option 1, as recommended

## Session 4 — draft-plan re-run (2 questions)

### Q32: Plan name confirmation
**Options:** 1. Yes, lms-mvp — 2. Different name
**Recommended:** Option 1
**Chosen:** Option 1, as recommended

### Q33: Repo setup (re-asked)
**Options:** 1. Disk-only draft again — 2. Init git + create GitHub repo now
**Recommended:** Option 1
**Chosen:** Option 1, as recommended (consistent with Q19)

## Session 5 — doubts review on Task 1 (2 questions)

### Q34: Admin dashboard owner
**Options:** 1. Fold into Task 6 as a final sub-scope — 2. New dedicated task — 3. Drop it from MVP
**Recommended:** Option 1 (Task 6 is where org/course/enrollment/payment data all first coexist)
**Chosen:** Option 1, as recommended — surfaced because Task 1 promised a basic admin dashboard as an MVP deliverable but no task (2-6) actually built it, and main-plan.md's own Goal statement listed "admin console" as out of scope, creating a direct contradiction. Task 6 was extended with an org-scoped + platform-owner cross-org read-only dashboard.

### Q35: Platform monetization
**Options:** 1. Take a commission on course sales — 2. Org subscription (platform bills organizations) — 3. Not monetized in MVP
**Recommended:** Option 1 (piggybacks on commerce already being built in Task 6, no new billing system needed)
**Chosen:** Option 1, as recommended — surfaced because Task 1's pricing-model line vaguely implied "paid plans for organizations" (i.e. platform-to-org billing) but no task built any such system. Task 6 was extended with server-side commission computation on every order, and Task 1's pricing-model line was rewritten to state the commission model plainly.

## Session 6 — grill-me on Task 2 (10 questions)

### Q36: Local Supabase setup
**Options:** 1. Supabase CLI local stack — 2. Shared cloud dev project — 3. Per-developer cloud project
**Recommended:** Option 1 (offline-capable, identical semantics to production, no cross-developer data collisions)
**Chosen:** Option 1, as recommended

### Q37: Worker binary shape
**Options:** 1. Single binary, subcommands — 2. Two separate binaries/images
**Recommended:** Option 1 (shared config/build/CI, less complexity for MVP)
**Chosen:** Option 1, as recommended

### Q38: Reverse proxy choice
**Options:** 1. Caddy — 2. nginx
**Recommended:** Option 1 (Caddy — automatic HTTPS, less manual TLS config for a single VPS)
**Chosen:** Option 2, against the recommendation: user chose nginx despite Caddy's simpler auto-TLS tradeoff

### Q39: Local video/bunny.net
**Options:** 1. Real bunny.net test/sandbox credentials — 2. Stub/mock video storage locally
**Recommended:** Option 1 (keeps local dev behavior identical to production, avoids masking integration bugs)
**Chosen:** Option 1, as recommended

### Q40: Rate limiting ownership
**Options:** 1. Task 2 builds generic middleware, Task 3 applies it — 2. Leave entirely to Task 3
**Recommended:** Option 1 (reuses Task 2's Redis, avoids Task 3 building limiter infra from scratch)
**Chosen:** Option 1, as recommended

### Q41: Test coverage baseline
**Options:** 1. Drop the hard threshold for MVP — 2. Keep 50% as a floor
**Recommended:** Option 1 (arbitrary number with no real baseline yet; report coverage instead of gating on it)
**Chosen:** Option 1, as recommended

### Q42: Feature flags mention
**Options:** 1. Yes, remove it — 2. Keep it as a placeholder
**Recommended:** Option 1 (same pattern as Task 1's admin-dashboard drift — don't promise a system nothing builds)
**Chosen:** Option 1, as recommended

### Q43: Secret scanning tool
**Options:** 1. gitleaks — 2. truffleHog
**Recommended:** Option 1 (simpler CI integration, no API key needed)
**Chosen:** Option 1, as recommended

### Q44: Redis durability
**Options:** 1. Durable in production too — 2. Ephemeral is fine
**Recommended:** Option 1 (Task 6's payment webhook jobs and email sends are queued here; losing them on restart is a real problem)
**Chosen:** Option 1, as recommended

### Q45: Signing key rotation line
**Options:** 1. Yes, remove it — 2. Keep it, clarify meaning instead
**Recommended:** Option 1 (no Go-side signing key exists in this architecture — Supabase Auth signs JWTs, Go only verifies)
**Chosen:** Option 1, as recommended

## Session 7 — grill-me on Task 3 (10 questions)

### Q46: HTML vs JSON scope
**Options:** 1. JSON-API only for now, defer HTML/HTMX — 2. Build HTML+HTMX pages now
**Recommended:** Option 1 (keeps Task 3 focused on the identity/tenancy backend; a thin HTML layer can wrap the same API later)
**Chosen:** Option 2, against the recommendation: user wants HTML+HTMX pages built now, per the original spec

### Q47: Email verify gate
**Options:** 1. Verify before first login — 2. Login allowed, verification nagged later
**Recommended:** Option 1 (avoids unverified accounts creating orgs/spamming invitations before confirming ownership of the email)
**Chosen:** Option 1, as recommended

### Q48: Deletion cascade
**Options:** 1. Block deletion, require transfer first — 2. Auto-delete owned orgs (cascade) — 3. Auto-transfer to another member
**Recommended:** Option 1 (prevents orphaned/undeletable orgs and accidental data loss for other members)
**Chosen:** Option 1, as recommended

### Q49: Ownership transfer
**Options:** 1. Reuse existing role-change endpoint (allow multiple simultaneous owners) — 2. Dedicated transfer-ownership endpoint
**Recommended:** Option 1 (no new endpoint needed; the existing role-change endpoint already covers it)
**Chosen:** Option 1, as recommended

### Q50: Backoff formula
**Options:** 1. 5 fails/15min per IP + per-email lockout doubling to a 1-hour cap — 2. Pure per-attempt exponential delay
**Recommended:** Option 1 (bounded, easy to reason about and test)
**Chosen:** Option 1, as recommended

### Q51: API token TTL
**Options:** 1. Optional expiry, default none — 2. Mandatory expiry (e.g. 1 year max)
**Recommended:** Option 1 (matches common API-token UX, no forced-rotation flow needed for MVP)
**Chosen:** Option 1, as recommended

### Q52: Invite no-account
**Options:** 1. Accept link routes to register, then auto-joins — 2. Accept requires pre-existing account
**Recommended:** Option 1 (better UX, one continuous flow instead of two disconnected steps)
**Chosen:** Option 1, as recommended

### Q53: Invite email owner
**Options:** 1. Task 3 sends a plain transactional email now (via Task 2's Resend integration) — 2. Task 3 stubs it, Task 7 implements sending
**Recommended:** Option 1 (invitations must be fully functional end-to-end today; Task 7 can reskin later)
**Chosen:** Option 1, as recommended

### Q54: Moderator scope
**Options:** 1. Moderator = learner-equivalent for now — 2. Moderator can manage members (not org settings)
**Recommended:** Option 1 (keeps Task 3's permission matrix simple; only 'owner' has elevated rights; Task 7 defines moderator's real powers)
**Chosen:** Option 1, as recommended

### Q55: Org creation limits
**Options:** 1. No hard limit for MVP, rate-limited instead — 2. Hard cap (e.g. 3 orgs per user)
**Recommended:** Option 1 (avoids an arbitrary cap needing a support-ticket escape hatch; rate limiting blunts scripted abuse)
**Chosen:** Option 1, as recommended

## Session 8 — grill-me follow-up on Task 3 (6 questions)

Triggered by the HTML+HTMX decision in Session 7 (Q46), which opened new gaps the original spec didn't cover.

### Q56: Templating choice
**Options:** 1. Stdlib html/template + htmx — 2. templ (a-h/templ)
**Recommended:** Option 1 (zero new dependencies, matches the plan's stated stack literally)
**Chosen:** Option 1, as recommended

### Q57: CSRF protection
**Options:** 1. CSRF middleware on cookie-session routes — 2. Skip CSRF for MVP, rely on SameSite alone
**Recommended:** Option 1 (non-negotiable once cookies drive real mutations)
**Chosen:** Option 1, as recommended

### Q58: Cookie contents
**Options:** 1. Opaque session ID, server-side Redis lookup — 2. JWT directly in an HttpOnly cookie
**Recommended:** Option 1 (instant server-side logout, smaller XSS blast radius, JWT never touches the browser)
**Chosen:** Option 1, as recommended

### Q59: Org context precedence
**Options:** 1. URL slug always wins when present — 2. Session/header org always wins
**Recommended:** Option 1 (avoids ever silently acting on the wrong org)
**Chosen:** Option 1, as recommended

### Q60: Platform owner bootstrap
**Options:** 1. One-time CLI/migration command — 2. Env-var-driven auto-promotion on boot
**Recommended:** Option 1 (auditable, no standing privilege-escalation path left in the API surface)
**Chosen:** Option 1, as recommended

### Q61: Password policy
**Options:** 1. Defer to Supabase Auth defaults — 2. App enforces its own stricter policy
**Recommended:** Option 1 (avoids duplicating/drifting from validation Supabase already does)
**Chosen:** Option 1, as recommended
