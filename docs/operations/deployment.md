# Deployment (Staging & Production)

How to stand up and update a staging or production environment (plan.md Task
12). The stack is defined in [`docker-compose.prod.yml`](../../docker-compose.prod.yml):
`api` (HTTP server), `worker` (asynq background jobs), `redis`, and an `nginx`
TLS terminator ([`nginx/nginx.prod.conf`](../../nginx/nginx.prod.conf)). Postgres,
Auth, and object storage are Supabase-hosted, not in compose.

## Topology

```
Internet ──TLS──> nginx ──> api (:8080)
                              │
              worker ─────────┤ (shares Redis + Supabase)
                    redis ────┘
Supabase (Postgres / Auth / Storage) — external, managed
Bunny Stream (video) — external, managed
```

## Environments

Staging mirrors production with `LMS_ENV=staging`, its own Supabase project,
its own secrets, and **test-mode** payment/webhook keys. Never point staging at
production data or live payment keys. Each environment has its own `.env`
(never committed — see [secret-management.md](secret-management.md)).

## First-time provisioning

1. **DNS + TLS** — point the domain at the host. `nginx.prod.conf` expects
   Let's Encrypt certs at `/etc/letsencrypt/live/<domain>/`. Issue them (e.g.
   certbot) before starting nginx, and mount the cert volume into the nginx
   service.
2. **Secrets** — create `.env` with every `LMS_*` value (DB URL, Supabase keys,
   Razorpay, Bunny, Resend, Redis). See `.env.example`.
3. **Database** — run migrations against the target Supabase DB:
   `./bin/app migrate up` (or `docker compose -f docker-compose.prod.yml run --rm api migrate up`).
   Requires a **direct** Postgres connection (not the pooler) for DDL.
4. **Build & start** — `docker compose -f docker-compose.prod.yml up -d --build`.
5. **Verify** — see the launch checklist below.

## Updating (rolling deploy)

1. Build/pull the new image tag; set `LMS_IMAGE`.
2. `migrate up` first if the release adds migrations (migrations are
   forward-compatible with the previous app version).
3. `docker compose -f docker-compose.prod.yml up -d` — `restart: unless-stopped`
   plus the `api` healthcheck (`/healthz`) gives a rolling replace.
4. Confirm `/readyz` is green and error rate is flat.

Rollback: redeploy the previous image tag. Only run `migrate down` if the new
migration is known-safe to revert; prefer forward fixes.

## Launch checklist

- [ ] TLS valid, HTTP→HTTPS redirect works, HSTS header present (`curl -I`).
- [ ] `/healthz` 200, `/readyz` 200 on api; worker running.
- [ ] Migrations at expected version (`./bin/app migrate version`).
- [ ] A signed **test** payment webhook is accepted and grants entitlement
      (never granted from the browser redirect) — see `loadtest/webhook.js`.
- [ ] A file upload + a video upload complete end to end.
- [ ] Email (Resend) sends a real transactional message.
- [ ] `/privacy` and `/terms` render; `/manifest.webmanifest` and `/sw.js` load.
- [ ] Backups scheduled and a restore rehearsed ([backup-policy.md](backup-policy.md)).
- [ ] Monitoring/alerting live ([monitoring.md](monitoring.md)).
- [ ] Secrets are production values, rotation dates recorded.

## Related runbooks

- [Backups & restore](backup-policy.md)
- [Migrations & upgrades](upgrade-migration.md)
- [Secret management & rotation](secret-management.md)
- [Monitoring](monitoring.md) · [Incident response](incident-response.md)
