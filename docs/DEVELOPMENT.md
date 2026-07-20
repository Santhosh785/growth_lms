# Development setup

## Services and where they run

| Service              | Where it runs                                   |
|----------------------|--------------------------------------------------|
| Postgres, Auth, Storage | Local Supabase CLI stack (`supabase start`), Docker under the hood but managed by the CLI, not by our `docker-compose.yml` |
| API, worker, Redis, nginx | Our `docker-compose.yml` (this repo)         |
| bunny.net            | Remote — real sandbox/dev credentials, no local stub |
| Resend               | Remote |
| Razorpay             | Remote (scaffolded only, unused until Task 6) |

## One-time setup

1. Install the [Supabase CLI](https://supabase.com/docs/guides/cli).
2. Install [Docker](https://docs.docker.com/get-docker/) and Docker Compose.
3. Clone this repo and `cp .env.example .env`.

## Every-day workflow

1. Start the local Supabase stack:

   ```bash
   supabase start
   ```

   This prints a Postgres connection string, an API URL, and anon/service-role
   keys. Copy them into `.env` as `LMS_DATABASE_URL`, `LMS_SUPABASE_URL`,
   `LMS_SUPABASE_ANON_KEY`, and `LMS_SUPABASE_SERVICE_ROLE_KEY`.

2. Fill in the remaining `.env` values: a bunny.net sandbox storage zone/API
   key (ask the team lead for shared dev credentials, or create your own
   low-cost sandbox zone), and a Resend API key.

3. Run migrations against the local Supabase database:

   ```bash
   make migrate-up
   ```

4. Start the application stack (API, worker, Redis, nginx):

   ```bash
   docker compose up -d
   ```

5. The app is reachable directly at `http://localhost:8080` (hot-reload via
   `air`) or through nginx at `http://localhost:8081`.

6. Stop everything with `docker compose down` and, separately, `supabase stop`.

## Port mappings

| Port  | Service                        |
|-------|---------------------------------|
| 8080  | API (direct, hot-reload)        |
| 8081  | nginx (proxied)                 |
| 6379  | Redis                           |
| 54321 | Supabase API (Auth/Storage/REST), from `supabase start` |
| 54322 | Supabase Postgres, from `supabase start` |

## Production deployment (VPS)

Production uses `docker-compose.yml` + `docker-compose.prod.yml` together:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

TLS is terminated by nginx using certificates issued by certbot
(Let's Encrypt). This is a manual, one-time setup on the VPS:

```bash
sudo certbot certonly --webroot -w /var/www/certbot -d lms.example.com
```

Certificates renew via certbot's own cron/systemd timer, already installed
by the certbot package; nginx just needs a reload after renewal
(`docker compose exec nginx nginx -s reload`), which certbot's renewal
hooks can trigger.
