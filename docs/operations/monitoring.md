# Monitoring & Observability

What the platform exposes and what to watch in production (plan.md Task 12).

## Endpoints

| Endpoint | Purpose | Use |
| --- | --- | --- |
| `GET /healthz` | Liveness — process is up | Container healthcheck, load balancer |
| `GET /readyz` | Readiness — Postgres **and** Redis reachable | Gate traffic; alert if failing |
| `GET /metrics` | Prometheus metrics (Task 10) | Scrape target |

`/metrics` and `/readyz` should not be exposed publicly — scrape them on the
internal network or protect them at the proxy.

## Metrics

The Prometheus registry (`internal/metrics`, mounted via `middleware.Metrics`)
exposes HTTP request counts/latency and panic counts, labeled by route. Build
dashboards for:

- **Request rate & latency** per route — watch p95/p99 on the launch-critical
  paths (catalog, player, checkout, webhook); baselines are in `loadtest/`.
- **Error rate** (5xx) per route.
- **Panics recovered** (`middleware.Recover`) — any nonzero is investigate-now.
- **Worker**: asynq queue depth, processing latency, failure/retry counts.

## System alerts (Task 10)

The app records operational alerts to `system_alerts` across six categories —
`jobs`, `webhooks`, `storage`, `database`, `auth`, `other` — at
`warning`/`critical` severity (see [security-hardening.md](security-hardening.md)
and the Task 10 admin surface). Wire these to your pager. Notable ones:

- **`webhooks` critical** — a payment webhook failed to record/enqueue. A
  paying learner may not have access. Page immediately.
- **`database` warning** — connection-pool trouble (throttled 1/5min).
- **`auth` warning** — brute-force threshold crossed on a login.
- **`storage` warning** — upload/certificate URL generation failed.
- **`jobs`** — background job failures.

## What to alert on

| Signal | Threshold | Severity |
| --- | --- | --- |
| `/readyz` failing | > 1 min | critical (page) |
| 5xx rate | > 1% sustained | critical |
| checkout/webhook p95 | over SLO (see loadtest) | warning |
| `webhooks` critical alert | any | critical (page) |
| panic count | any increase | warning |
| worker queue depth | growing unbounded | warning |
| cert expiry | < 14 days | warning |

## Logs

Structured JSON logs (slog) are request-ID correlated (`middleware.RequestID`);
a recovered panic is logged with its request ID. Ship them to a central log
store and pivot on request ID when chasing an alert.
