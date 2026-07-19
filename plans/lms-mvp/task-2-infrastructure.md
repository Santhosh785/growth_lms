---
task: 2
name: infrastructure
parallel_group: 2
depends_on: [1]
issue: TBD
---

# Task 2: Local Infrastructure, Configuration, and Deployment Foundation

## What to build

This task establishes the complete local development environment, typed configuration pipeline, and deployment foundation for the LMS MVP. No business logic is built here — only the scaffolding everything else runs on top of.

### Environment and Configuration

- **`.env.example` file(s)** with project-specific variable prefix (e.g., `LMS_*`), documenting all required and optional configuration:
  - Supabase: Postgres connection string (`LMS_DATABASE_URL`), Auth public/secret keys, Storage bucket name and keys
  - bunny.net: API key, storage zone, CDN URL for video delivery
  - Redis: connection URL (`LMS_REDIS_URL`) for job queue and caching
  - Resend: API key for email provider
  - Razorpay: public key and secret (scaffolded but unused in this task)
  - Application: port, base URL, environment name (`development`/`staging`/`production`)

- **Typed configuration loading in Go** (e.g., a `config` package with a struct) that:
  - Reads environment variables and `.env` files (using a library like `godotenv`)
  - Validates that all required fields are present and valid (connection strings parse correctly, ports are numeric, etc.)
  - Fails fast at application startup if validation fails
  - Supports separate development, staging, and production profiles (e.g., different log levels, CORS strictness, TLS requirements — see Environment-Specific Profiles below)
  - Does not expose secrets in error messages or structured logs

### Local Supabase Stack

Local development uses the **Supabase CLI's local stack** (`supabase start`), not a shared cloud project and not a bare Postgres container. This runs Postgres, Auth, and Storage locally in Docker via the Supabase CLI, giving every developer an identical, offline-capable environment with the same Auth/RLS semantics as production, with no risk of colliding with teammates' data. `.env.example` documents how to point the app at the CLI's local connection string/keys (printed by `supabase start`), and development docs explain the one-time `supabase` CLI install step.

Video uploads (bunny.net) during local development use **real bunny.net sandbox/development credentials** (a low-cost dedicated storage zone), not a stub — so local dev exercises real signed-URL upload/playback behavior identical to staging/production, rather than risking integration bugs hidden behind a mock.

### Docker Compose for Local Development

- **Compose stack** (`docker-compose.yml`) with three services (Postgres/Auth/Storage come from the Supabase CLI stack above, not Compose):
  - **API service**: Runs the Go application (built as a single binary — see "Single Binary, Multiple Entrypoints" below) in its default `serve` mode, on a development port, with hot-reload capability (using a tool like `air`), mounts source code as a volume, logs to stdout
  - **Worker service**: The same Go binary/image, run in `worker` mode instead of `serve` — consumes Redis-backed jobs (e.g., using `asynq`), scales independently via its own Compose entry despite sharing the image
  - **Redis**: Latest stable version, persisted volume (AOF or RDB) for local convenience — see "Redis Durability" below for why this also applies in production
  - **Reverse proxy** (nginx): Routes requests, handles CORS headers, forwards `X-Forwarded-*` headers to the application, terminates TLS for staging/production testing (TLS via certbot/Let's Encrypt on the VPS, configured manually — not automatic)

- **Development documentation** explaining:
  - How to install and run the Supabase CLI locally (`supabase start`) and where its connection string/keys come from
  - How to populate `.env` locally (including bunny.net sandbox credentials)
  - Which services are local (Docker Compose: API, worker, Redis, nginx) vs. local-but-CLI-managed (Supabase stack) vs. remote (bunny.net, Resend, Razorpay)
  - Port mappings and how to access each service

### Single Binary, Multiple Entrypoints

The Go application is built as **one binary** with subcommands (e.g. `./app serve` for the HTTP API/HTML server, `./app worker` for the Redis job consumer), not separate binaries or separate Dockerfiles. One Dockerfile builds the single image; the API and worker Compose/production services both run that image with a different command/entrypoint argument. This keeps config loading, dependency wiring, and the build/CI pipeline single-sourced.

### Database Migrations

- **golang-migrate integration**:
  - Migrations directory (`db/migrations`) with version-numbered SQL files (e.g., `000001_init.up.sql`, `000001_init.down.sql`)
  - Make target or CLI command to run migrations: `make migrate-up`, `make migrate-down`, `make migrate-fresh`
  - Migrations run against the Supabase Postgres connection string (not a local Postgres container)
  - At least one trivial initial migration that proves the pipeline works (e.g., creating a `schema_version` table or similar sanity check)

### Health and Readiness Checks

- **`/healthz` endpoint** (liveness check):
  - Returns `200 OK` if the application is running
  - Simple, does not check dependencies

- **`/readyz` endpoint** (readiness check):
  - Returns `200 OK` only when the application is ready to serve traffic
  - Verifies: Postgres connection is healthy (via a lightweight query), Redis is reachable, all required configuration is loaded and valid
  - Returns `503 Service Unavailable` if any dependency fails
  - Used by the reverse proxy (and Kubernetes later) to route traffic only to healthy instances

### Logging and Observability

- **Structured JSON logging**:
  - Each HTTP request receives a unique request ID (UUID or correlation ID)
  - Request ID is threaded through all logs for that request (middleware)
  - JSON format with fields: `timestamp`, `level`, `message`, `request_id`, `duration_ms`, and contextual fields (user_id, action, etc. once available)
  - Development mode can use human-readable output; production strictly JSON

### CORS and Proxy Configuration

- **CORS configuration** suitable for a server-rendered app that also serves a JSON API:
  - Define trusted origins (configurable per environment)
  - Allow credentials and standard methods (GET, POST, PUT, DELETE)
  - Correct headers for preflight requests

- **Reverse proxy configuration** (nginx):
  - Correctly forwards `X-Forwarded-For`, `X-Forwarded-Proto`, `X-Forwarded-Host` to the Go application
  - Application trusts these headers from the proxy (configurable)
  - Handles cookie domain/path correctly when proxying
  - TLS is terminated at nginx using certificates obtained via certbot (Let's Encrypt), renewed via a cron/systemd timer on the VPS — this is a manual setup step for Task 2, unlike an auto-TLS proxy

### Generic Rate-Limiting Middleware

Build a reusable, Redis-backed rate-limiting middleware (e.g. token-bucket or sliding-window, keyed by IP and/or identity) as infrastructure — this task does NOT apply it to any specific route. Task 3 configures and attaches it to auth endpoints (login/register/password-reset) with its own specific limits; later tasks may reuse it elsewhere. Task 2's job is only to make a working, configurable rate limiter available; it has no opinion on which routes need it or what their limits should be.

### Environment-Specific Profiles

- **Development**:
  - Relaxed CORS (or localhost-only)
  - Hot-reload enabled
  - Verbose logging
  - Redis reachable locally via Docker Compose; Postgres/Auth/Storage via the local Supabase CLI stack

- **Staging**:
  - Full TLS (via nginx + certbot)
  - Harder CORS (specific origins only)
  - Rate-limiting middleware active (Task 3 attaches it to auth routes; see above)
  - Logging at info level
  - Integration with a staging Supabase cloud project

- **Production**:
  - TLS only
  - Strict CORS and security headers (CSP, HSTS, etc.)
  - Error messages sanitized (no stack traces to clients)
  - Structured logging only
  - Integration with production Supabase cloud project
  - No hot-reload, no debug endpoints
  - Redis persistence (AOF or RDB) enabled — queued jobs (email sends, Task 6's payment webhook processing) must survive a Redis restart, not just be treated as best-effort

### CI Pipeline

- **GitHub Actions workflow** (`.github/workflows/ci.yml`) running on every push and pull request:
  - **Go formatting**: `gofmt` or equivalent; fail if code is not formatted
  - **Linting**: `golangci-lint` (or similar) with strict rules
  - **Unit tests**: `go test ./...` with race detector enabled (`-race`); coverage is measured and reported (e.g. via a CI annotation/artifact) but does NOT gate the pipeline with a hard percentage threshold for MVP — there's no meaningful baseline yet, and an arbitrary number invites either shallow tests written to hit it or blocking legitimate PRs. Revisit a real threshold once Task 3+'s security-critical test suite exists.
  - **Migration validation**: Attempt to run the migration pipeline in a dry-run mode against an ephemeral Postgres service container spun up just for this CI job (this is a CI-only convenience, not a statement about local dev — local dev still uses the Supabase CLI stack, never a bare Postgres container) to catch SQL syntax errors
  - **Secret scanning**: `gitleaks`, run via its official GitHub Action, to detect accidentally committed secrets
  - **Docker build check**: Verify that both the development and production Dockerfiles build successfully

- All checks must pass before a PR can be merged.

### Production Docker Images

- **Dockerfile** (multi-stage if beneficial):
  - Builds the Go application statically (no runtime dependencies)
  - Production image includes ONLY the compiled binary and runtime certificates
  - No source code, no `.env` files, no development tools in the final image
  - Development secrets (e.g., test database URLs) are never baked into any image
  - Follows best practices: non-root user, minimal base image (distroless or alpine)

- **Docker Compose override** for production VPS deployment:
  - Pulls pre-built images (from a registry or built on the VPS)
  - Configures the reverse proxy for external traffic (port 80/443)
  - Disables volume mounts (code is in the image, not mounted)
  - Defines restart policies and health checks

### Makefile/CLI Targets

Provide a `Makefile` or equivalent with targets for:
- `make docker-build`: Build Docker images locally
- `make docker-up`: Start the full local development stack
- `make docker-down`: Stop the stack
- `make migrate-up`, `make migrate-down`, `make migrate-fresh`: Run database migrations
- `make lint`: Run linters
- `make test`: Run unit tests
- `make fmt`: Format code
- `make ci`: Run all CI checks locally (formatting, lint, test, migrations, secrets)

## Acceptance criteria

- [ ] A new developer can start the full local stack (`supabase start` for the local Supabase CLI stack, then `docker compose up -d` for API/worker/Redis/nginx) after cloning the repo and populating `.env` from `.env.example`, using a single documented sequence of commands.

- [ ] The API container successfully connects to the local Supabase CLI stack's Postgres, Redis, and loads all external provider credentials (bunny.net sandbox, Resend, Razorpay) via typed configuration validation, without errors.

- [ ] The worker container runs the same image as the API container (via a different entrypoint command) and successfully consumes jobs from Redis.

- [ ] Production Docker images build successfully with no development secrets, no source code, and no build artifacts included; images can be scanned by a security scanner.

- [ ] The `/healthz` and `/readyz` endpoints correctly return `200 OK` when all dependencies are healthy, and `503 Service Unavailable` when Postgres or Redis is unreachable.

- [ ] `golang-migrate` successfully runs the initial sanity migration against a fresh Supabase database (local CLI stack or a real Supabase project), creating the migration tracking table without error.

- [ ] The GitHub Actions CI pipeline runs on every push/PR, executing formatting checks, linting, unit tests (with coverage reported, not gated), migration validation (against an ephemeral CI-only Postgres container), and gitleaks secret scanning; all checks pass with the scaffolded code.

- [ ] nginx correctly forwards `X-Forwarded-*` headers to the Go application, terminates TLS via certbot-issued certificates, and CORS headers are configurable per environment without code changes.

- [ ] Structured JSON logging is active in production mode, including request IDs and durations; no sensitive configuration is logged.

- [ ] A generic Redis-backed rate-limiting middleware is available and configurable, ready for Task 3 to attach to auth endpoints — this task does not apply it to any route itself.

- [ ] Redis is configured with persistence (AOF or RDB) enabled in the production Compose override, not just as a local-dev convenience.

## Commit convention

Your commit message MUST include `Closes #<issue-number>` (issue number to be filled in when published to GitHub) when the task's GitHub issue closes.
