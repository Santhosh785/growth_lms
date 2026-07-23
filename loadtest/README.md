# Load tests

k6 load tests for the four launch-critical endpoint families called out in
plan.md Task 11: **catalog**, **player**, **checkout**, and **webhook**.

Each script defines SLO thresholds (p95 latency + error rate) in its
`options.thresholds`, so `k6 run` exits non-zero when an SLO is breached —
these are gates, not just reports.

## Prerequisites

Install [k6](https://grafana.com/docs/k6/latest/set-up/install-k6/):

```sh
# macOS
brew install k6
# Debian/Ubuntu
sudo apt-get install k6
```

Run against **staging or a dedicated load environment** — never production.
`webhook.js` and `checkout.js` create real rows (webhook events, orders).

## Configuration

All targets are environment variables (see `lib.js` for the full list):

| Var | Used by | Meaning |
| --- | --- | --- |
| `BASE_URL` | all | Base origin (default `http://localhost:8080`) |
| `ORG_SLUG` | catalog, checkout | Org slug |
| `COURSE_ID` | player, checkout | Course UUID |
| `OFFER_ID` | checkout | Offer UUID |
| `AUTH_TOKEN` | player, checkout | Learner session/bearer token |
| `CSRF_TOKEN` | checkout | CSRF token matching the session |
| `WEBHOOK_SECRET` | webhook | Razorpay webhook secret (non-prod) |
| `VUS` / `RAMP_UP` / `HOLD` / `RAMP_DOWN` | all | Override the load profile |

## Running

```sh
# Public catalog (no auth needed)
k6 run -e BASE_URL=https://staging.example.com -e ORG_SLUG=acme loadtest/catalog.js

# Authenticated player
k6 run -e BASE_URL=https://staging.example.com \
       -e AUTH_TOKEN=$TOKEN -e COURSE_ID=$CID loadtest/player.js

# Checkout order creation
k6 run -e BASE_URL=https://staging.example.com \
       -e AUTH_TOKEN=$TOKEN -e CSRF_TOKEN=$CSRF \
       -e COURSE_ID=$CID -e OFFER_ID=$OID loadtest/checkout.js

# Signed payment webhook
k6 run -e BASE_URL=https://staging.example.com \
       -e WEBHOOK_SECRET=$SECRET loadtest/webhook.js

# Heavier soak
k6 run -e VUS=200 -e HOLD=10m -e BASE_URL=... loadtest/catalog.js
```

## SLOs

| Script | p95 latency | Error rate |
| --- | --- | --- |
| catalog | < 300 ms | < 1% |
| webhook | < 400 ms | < 1% (429 tolerated) |
| player | < 500 ms | < 1% |
| checkout | < 700 ms | < 1% (409 tolerated) |

Adjust the numbers in each script's `thresholds(...)` call as real baselines
are established on the target hardware.
