---
task: 3
name: auth-tenancy
parallel_group: 3
depends_on: [2]
issue: TBD
---

# Task 3: Authentication, Organizations, Tenancy, and Permissions

## What to build

This task establishes the identity and multi-tenancy foundation for the platform. It creates the user authentication system, organization model, membership and role infrastructure, and permission-enforcement layer that all later tasks (courses, media, commerce) will depend on. All work must follow the locked-in architecture patterns described below.

### Architecture & Constraints

**Identity source of truth:** Supabase Auth (its `auth.users` table is the single source of truth). The Go backend does NOT implement its own password/session system—instead, it verifies JWTs issued by Supabase and layers a `profiles`/organization/role model on top, linked by Supabase user ID.

**Tenant isolation:** Enforced at the database level via Postgres Row-Level Security (RLS). Every organization-owned table gets an `org_id` column AND a Postgres RLS policy. This is defense-in-depth and non-negotiable—RLS is the isolation boundary, not just app-layer filtering. A user in org A physically cannot read or mutate org B's rows, even if the Go code has a bug.

**Session variables for RLS:** Go connects directly to Postgres via pgx and issues `SET LOCAL` session variables at the start of each request's transaction, setting `current_user_id`, `current_org_id`, and `current_role` so RLS policies can read them via `current_setting(...)`. Every authenticated request must set these before running any org-scoped query.

**Database migrations:** Managed via golang-migrate (per Task 2).

**Auth scope (MVP only):** Email/password authentication only. OAuth (Google), MFA, and social login are explicitly deferred to a later phase—do not build them now.

**Multi-tenancy scope:** This is a public multi-tenant SaaS from MVP launch, not a single-organization installation. Any authenticated user may create a new organization (self-service) and becomes its owner—organization creation is NOT gated behind platform-owner approval. Multiple organizations coexist in the same database from day one, isolated entirely by the RLS policies described below. `profiles.is_platform_owner` is a separate, global flag independent of any organization membership—it identifies the operator of the platform itself, not a per-org role.

### Database Schema & RLS

Create the following tables with their RLS policies:

- **`profiles`**: (1:1 linked to `auth.users.id`)
  - `id` (UUID, PK, foreign key to auth.users.id)
  - `email` (TEXT, indexed)
  - `full_name` (TEXT, nullable)
  - `avatar_url` (TEXT, nullable)
  - `is_platform_owner` (BOOLEAN, default false)
  - `created_at`, `updated_at` (TIMESTAMP)
  - RLS policy: User can read/update their own profile; platform owners can read all.

- **`organizations`**:
  - `id` (UUID, PK)
  - `slug` (TEXT, unique, used in URLs)
  - `name` (TEXT)
  - `created_by_user_id` (UUID, foreign key to profiles.id)
  - `created_at`, `updated_at` (TIMESTAMP)
  - RLS policy: Only org members (via memberships table) can read; only org owners can update/delete.

- **`memberships`**: (join table for users × orgs × roles)
  - `id` (UUID, PK)
  - `user_id` (UUID, foreign key to profiles.id)
  - `org_id` (UUID, foreign key to organizations.id)
  - `role` (TEXT: 'owner', 'teacher', 'learner', 'moderator')
  - `joined_at` (TIMESTAMP)
  - Unique constraint: (user_id, org_id)
  - RLS policy: Only members of the org can read; only org owners can insert/update/delete.

- **`invitations`**:
  - `id` (UUID, PK)
  - `org_id` (UUID, foreign key to organizations.id)
  - `email` (TEXT)
  - `role` (TEXT: 'teacher', 'learner', 'moderator')
  - `invited_by_user_id` (UUID, foreign key to profiles.id)
  - `status` (TEXT: 'pending', 'accepted', 'declined')
  - `token` (TEXT, unique, secret token for accepting without login)
  - `created_at`, `expires_at` (TIMESTAMP)
  - RLS policy: Only org members can read/create; only org owners can delete; invited users can accept/decline their own invitations.

- **`audit_events`**:
  - `id` (UUID, PK)
  - `org_id` (UUID, foreign key to organizations.id, nullable for platform-level events)
  - `user_id` (UUID, foreign key to profiles.id, nullable for system events)
  - `action` (TEXT: 'login', 'logout', 'password_change', 'user_created', 'invitation_sent', 'role_changed', 'member_removed', 'org_created', etc.)
  - `resource_type` (TEXT: 'user', 'organization', 'membership', etc.)
  - `resource_id` (UUID, nullable)
  - `details` (JSONB, nullable—captures mutation details or context)
  - `ip_address` (INET, nullable)
  - `user_agent` (TEXT, nullable)
  - `created_at` (TIMESTAMP)
  - RLS policy: Users can read events from their orgs; platform owners can read all.

### Authentication Flows

Implement email/password registration, email verification, login, password reset, logout, and account deletion. These flows use Supabase Auth's native APIs under the hood but are exposed via:

- **HTML pages + HTMX endpoints:** Login page, register page, password-reset request page, password-reset confirmation page, email-verification page, account settings page. Rendered with Go's stdlib `html/template` (no third-party templating library), progressively enhanced with htmx. All state-changing HTML/HTMX form routes are protected by CSRF middleware (e.g. a double-submit-cookie or `gin-contrib/csrf`-style token); JSON bearer-token API routes are exempt, since they carry no ambient cookie auth.
- **JSON API endpoints** (for future mobile/third-party clients):
  - `POST /api/auth/register` — register, returns JWT or verification-required response.
  - `POST /api/auth/verify-email` — confirm email via token.
  - `POST /api/auth/login` — login, returns JWT.
  - `POST /api/auth/password-reset-request` — request reset, sends email.
  - `POST /api/auth/password-reset` — complete reset with token.
  - `POST /api/auth/logout` — revoke session/token.
  - `POST /api/auth/delete-account` — delete user; blocked (409, listing the orgs) if the user is a sole owner of any organization (see Ownership below).

**Email verification is mandatory before first login.** Registration always returns a verification-required response (never a JWT); `POST /api/auth/login` rejects unverified accounts with a clear error. This applies to org self-service creation too — an unverified user cannot create or join an organization, since doing so requires being logged in.

**Password policy: defer entirely to Supabase Auth's defaults** — the Go app adds no independent password-strength validation; the registration HTML form may surface Supabase's minimum as a UI hint, but the source of truth for what's accepted is Supabase Auth itself.

**Platform-owner bootstrap:** the first `profiles.is_platform_owner = true` row is set via a one-time `serve bootstrap-owner --email=<email>` CLI subcommand (same single-binary `serve`/`worker` pattern as Task 2), promoting an existing verified profile. Run manually once during initial deployment setup; the promotion is logged to `audit_events`. There is no API endpoint or env-var auto-promotion path — no standing privilege-escalation surface is left in the running server.

Rate-limit login, registration, and password-reset endpoints to prevent brute force (e.g., 5 attempts per 15 minutes per IP).

### JWT Verification Middleware

Create a middleware that:

1. Extracts JWT from `Authorization: Bearer <token>` header (JSON API) or resolves it from the session cookie (HTML flows). The HTML session cookie holds an opaque, random session ID only — never the JWT itself; the ID maps in Redis to the stored Supabase JWT/refresh-token pair. This keeps logout an instant server-side delete and the JWT off the wire to the browser, shrinking the XSS blast radius versus a JWT-bearing cookie.
2. Verifies the JWT signature using Supabase's public key.
3. Resolves the JWT's `sub` claim to a `profiles` row.
4. Determines the caller's organization context. **Precedence when signals conflict:** for routes with an `:org_slug` in the URL, the slug is always authoritative — the middleware resolves the caller's role fresh from `memberships` for that org (even if it differs from the session's "last active org") and returns 403 if they're not a member. Session/header-based org context is used only for routes with no org in the URL (e.g. a generic `/dashboard` redirecting to the user's default org).
5. Resolves the caller's role within that org (from `memberships` table).
6. Issues Postgres session variables: `SET LOCAL app.current_user_id = '<user_id>'`, `SET LOCAL app.current_org_id = '<org_id>'`, `SET LOCAL app.current_role = '<role>'`.
7. Passes the context (user ID, org ID, role) to downstream handlers.

This middleware must work for both HTML/cookie-session and JSON bearer-token flows.

### Organization & Membership Management

Implement CRUD and flow endpoints:

- **Organization CRUD:**
  - `POST /api/orgs` — create org, set creator as owner (requires auth, requires a verified email). No hard cap on how many orgs a user may own for MVP; the endpoint is covered by the same rate limiter as auth endpoints (e.g., 5 creations per hour per user) to blunt scripted abuse.
  - `GET /api/orgs/:org_slug` — fetch org details.
  - `PATCH /api/orgs/:org_slug` — update org name/settings (org owner only).
  - `DELETE /api/orgs/:org_slug` — delete org and cascade (org owner only).

- **Membership management:**
  - `GET /api/orgs/:org_slug/members` — list members (org members only).
  - `PATCH /api/orgs/:org_slug/members/:user_id/role` — change user's role (org owner only). An org may have multiple simultaneous owners (`memberships.role = 'owner'` is not unique per org). This endpoint doubles as the ownership-transfer mechanism: an owner promotes another member to `'owner'`, then may demote themselves or remove themselves — no separate transfer-ownership endpoint is needed.
  - `DELETE /api/orgs/:org_slug/members/:user_id` — remove user (org owner only, cannot remove self).

- **Invitation flow:**
  - `POST /api/orgs/:org_slug/invitations` — send invitation by email (org owner/teacher only). Sends a plain transactional email (via Task 2's Resend integration) containing the accept link — invitations must be fully functional end-to-end in this task; Task 7 (Communities) may later restyle the template.
  - `GET /api/orgs/:org_slug/invitations` — list pending invitations (org members only).
  - `POST /api/invitations/:token/accept` — accept invitation (no auth required, token validates). If no `profiles` row exists yet for the invited email, respond with a `registration_required` status (the HTML flow redirects to registration pre-filled with the invited email); once the account is created and its email verified, the pending invitation auto-accepts and creates the membership.
  - `POST /api/invitations/:token/decline` — decline invitation (no auth required).
  - `DELETE /api/orgs/:org_slug/invitations/:invitation_id` — revoke invitation (org owner only).

All operations enforce role-based permissions server-side, not just in the UI.

**Moderator role scope (this task only):** `'moderator'` is accepted as a valid role value in schema, invitations, and role-changes, but in Task 3's permission matrix it carries no rights beyond `'learner'` — only `'owner'` has elevated org/membership/API-token management rights. Task 7 (Communities) defines and adds moderator-specific permissions (discussion moderation) later, without requiring schema changes here.

### Permission Middleware & Helper

Provide a reusable middleware/helper (e.g., `RequireRole(role string)` or `CanActOn(resource_type, action string)`) that:

- Checks the caller's role against required permissions.
- Returns 403 Forbidden if unauthorized.
- Is idempotent and composable so Task 4/5/6 can chain it for their own domain checks (e.g., "only teacher can create a course").

Document the permission model clearly (matrix of role → actions) so later tasks can extend it.

### API Tokens for Machine-to-Machine

Implement API tokens scoped to an organization for third-party integrations:

- `POST /api/orgs/:org_slug/api-tokens` — create token (org owner only), returns secret once. Accepts an optional `expires_at`; if omitted, the token never expires (revoke-only, matching common API-token UX like GitHub PATs).
- `GET /api/orgs/:org_slug/api-tokens` — list tokens (org members only).
- `DELETE /api/orgs/:org_slug/api-tokens/:token_id` — revoke token (org owner only).

Tokens are validated the same way as JWTs (set session variables) but log a different audit event.

### Rate Limiting & Abuse Protection

- Rate-limit auth endpoints: login/register/password-reset to 5 attempts per 15 minutes per IP (using Task 2's generic Redis-backed rate limiter).
- **Per-email lockout on failed logins:** independent of the per-IP limit above, after 5 consecutive failed logins to the same email (regardless of source IP), lock that email out for 15 minutes; each subsequent lockout within the same failure streak doubles the lockout duration, capped at 1 hour. The streak (and its backoff level) resets on a successful login.
- Log rate-limit violations and lockouts to audit_events for alerting.

### Audit Logging

Write audit events for:

- User registration, email verification, login, logout, password change, account deletion.
- Organization creation, update, deletion.
- Membership changes: invitation sent, accepted, declined, role changed, user removed.
- API token creation and revocation.
- Unauthorized access attempts (e.g., permission denied).

Each audit event includes user ID, org ID (if applicable), action, timestamp, IP, and user-agent.

### Testing Requirements

This task requires automated tests (not deferred to later hardening):

1. **Tenant-isolation tests:**
   - User A in org 1 attempts to read org 2's organizations/memberships/invitations/audit_events rows.
   - Verify via real RLS policies (not mocked), not just application-code assertions.
   - User A should receive 0 rows or a 403 error.

2. **Permission tests:**
   - Learner attempts to invite users to org → 403.
   - Non-owner attempts to change another user's role → 403.
   - Moderator attempts to delete org → 403.
   - Test all major role/action combinations.

3. **Auth flow tests:**
   - Registration with invalid email → error.
   - Registration with existing email → error.
   - Email verification with invalid token → error.
   - Login with wrong password → error.
   - Password reset flow end-to-end.
   - Logout invalidates token.

4. **Membership and invitation tests:**
   - Invite user, accept invitation, user joins org, has correct role.
   - Invite user, decline invitation, user is not a member.
   - Remove member, user no longer has access.

## Acceptance criteria

- [ ] Database schema for `profiles`, `organizations`, `memberships`, `invitations`, and `audit_events` is migrated via golang-migrate.
- [ ] All tables have RLS policies enforcing organization isolation at the database level.
- [ ] A user in org A cannot read or mutate org B's data—proven by an automated test exercising real RLS policies.
- [ ] Email/password registration, email verification, login, password reset, logout, and account deletion all work end-to-end against Supabase Auth.
- [ ] JWT verification middleware correctly validates Supabase-issued tokens and resolves user/org/role context.
- [ ] Postgres session variables (`current_user_id`, `current_org_id`, `current_role`) are set before every authenticated request, enabling RLS policies to work.
- [ ] Organization CRUD (create, read, update, delete) works with proper ownership checks; any authenticated user can create a new organization (public self-service signup), becoming its owner, without platform-owner approval.
- [ ] Membership management (list, change role, remove) enforces org-owner-only permissions server-side.
- [ ] Organization invitation flow (send, accept, decline, revoke) works end-to-end and is enforced server-side.
- [ ] Role-based permission checks are enforced server-side for every protected operation, not only hidden in the UI—proven by tests that attempt unauthorized actions directly against the API.
- [ ] API tokens can be created, listed, and revoked with proper scoping and audit logging.
- [ ] Rate limiting and abuse protection are in place for auth endpoints (login, register, password-reset).
- [ ] Audit events are logged for all security-relevant and administrative actions (login, role change, invitation, deletion, etc.).
- [ ] Automated tests prove tenant isolation for every table created in this task.
- [ ] Automated tests prove role-based permission enforcement for major role/action combinations.

## Commit convention

Your commit message MUST include `Closes #<issue-number>` when the task's GitHub issue closes. All commits should follow this format:

```
<type>(<scope>): <subject>

<body (optional)>

Closes #<issue-number>
Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
```

Example:
```
feat(auth): implement JWT verification middleware

Add middleware to validate Supabase-issued JWTs, resolve user/org/role context,
and set Postgres session variables for RLS policies.

Closes #42
Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>
```
