# Secret Management & Rotation

This document defines how the platform's secrets are stored, who can access
them, and the exact procedure for rotating each one (plan.md Task 11 release
gate: "Secret management and rotation"). It is the "out of band; see Secret
management" reference called out in the [backup policy](backup-policy.md).

## Principles

- **Never in git.** Secrets live only in the deployment's secret store (the
  hosting provider's environment/secret manager), never in the repo. CI's
  `gitleaks` job (`.github/workflows/ci.yml`) fails the build if a secret is
  committed.
- **Injected as environment variables** at runtime. The app reads every secret
  through `internal/config` at startup (`LMS_*` variables); it never reads a
  secrets file from disk.
- **Least privilege.** Only the production deploy role and named on-call
  operators can read production secrets. Rotation is logged out of band.
- **Separate per environment.** Development, staging, and production each have
  their own distinct values. A leaked staging key never grants production
  access.

## Secret inventory

| Env var | What it is | Provider | Rotatable without downtime? |
| --- | --- | --- | --- |
| `LMS_DATABASE_URL` | Postgres connection string (contains password) | Supabase | Yes — dual-password window |
| `LMS_REDIS_URL` | Redis connection string | Redis host | Yes — brief reconnect |
| `LMS_SUPABASE_JWT_SECRET` | Verifies Supabase-issued session JWTs | Supabase | **No — invalidates all sessions** |
| `LMS_SUPABASE_SERVICE_ROLE_KEY` | Server-side admin access to Supabase | Supabase | Yes |
| `LMS_SUPABASE_ANON_KEY` | Public anon key | Supabase | Yes |
| `LMS_RAZORPAY_KEY_ID` / `LMS_RAZORPAY_KEY_SECRET` | Payment API credentials | Razorpay | Yes |
| `LMS_RAZORPAY_WEBHOOK_SECRET` | Verifies payment webhook signatures | Razorpay | Yes — dual-secret window |
| `LMS_BUNNY_API_KEY` | Bunny Stream API access | Bunny | Yes |
| `LMS_BUNNY_WEBHOOK_SECRET` | Verifies transcode webhook signatures | Bunny | Yes |
| `LMS_RESEND_API_KEY` | Transactional email | Resend | Yes |
| `LMS_AI_API_KEY` / `ANTHROPIC_API_KEY` | LLM provider key | Anthropic | Yes |

## Rotation cadence

- **Routine:** every **90 days** for all API keys and webhook secrets.
- **Database & Redis passwords:** every **180 days**.
- **Immediately, out of cadence:** on any suspected compromise, when an
  operator with access leaves, or if a key appears in logs/a leak report.

## General rotation procedure

1. **Generate** the new value in the provider's console (or via the provider
   CLI). Do not delete the old value yet.
2. **Stage** the new value in the secret store as the live variable.
3. **Roll** the app: the new process reads the new value at startup. Deploy is
   a rolling restart, so old and new processes briefly coexist — which is why
   the dual-window notes below matter.
4. **Verify** health (`/readyz`) and a smoke test of the affected surface (a
   test payment webhook, a test upload, a login).
5. **Revoke** the old value at the provider once all instances run the new one.
6. **Record** the rotation (date, secret, operator) in the ops log.

## Per-secret notes

### `LMS_SUPABASE_JWT_SECRET` — session-breaking

Rotating this invalidates **every** active session (all JWTs signed with the
old secret fail `internal/auth` verification). Only rotate on compromise.
Schedule a maintenance window, communicate forced re-login, then rotate. There
is no zero-downtime path.

### Webhook secrets (`*_WEBHOOK_SECRET`) — dual-secret window

Payment/transcode webhook verification (`VerifyWebhookSignature`) accepts one
secret. To rotate without dropping in-flight webhooks:

1. Add the new secret at the provider so it signs new deliveries.
2. If the provider supports listing both, keep the old active during the
   window; otherwise expect a short window where old-signed retries may fail
   and rely on provider retries + the `webhook_event` dedup to reconcile.
3. Switch `LMS_*_WEBHOOK_SECRET` to the new value and roll.
4. Remove the old secret at the provider.

Because a dropped payment webhook means a paying learner never gets access,
rotate webhook secrets during low-traffic hours and confirm with a signed test
event (see `loadtest/webhook.js` for producing one against staging).

### `LMS_DATABASE_URL` — dual-password window

Supabase supports setting a new password while the old still works briefly.
Set the new password, update `LMS_DATABASE_URL`, roll the app, confirm
`/readyz` is green, then retire the old password.

### Razorpay / Bunny / Resend / AI keys

Standard procedure: create a second key, deploy it, revoke the first. These
providers allow multiple active keys, so rotation is zero-downtime.

## Verification checklist after any rotation

- [ ] `/readyz` returns 200 on all instances.
- [ ] A login succeeds (JWT/session path).
- [ ] A signed test webhook is accepted (payment path).
- [ ] A file upload completes end to end (storage path).
- [ ] No secret-related errors in logs; no `auth`/`database` system alerts
      firing (Task 10 alerting).
- [ ] Old value revoked at the provider.
- [ ] Rotation recorded in the ops log.
