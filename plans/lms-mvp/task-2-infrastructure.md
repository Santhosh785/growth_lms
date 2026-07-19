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
  - Supports separate development, staging, and production profiles (e.g., different log levels, feature flags)
  - Does not expose secrets in error messages or structured logs

### Docker Compose for Local Development

- **Compose stack** (`docker-compose.yml`) with four services:
  - **API service**: Runs the Go application on a development port, with hot-reload capability (using a tool like `air`), mounts source code as a volume, logs to stdout
  - **Worker service**: Separate Go application consuming Redis-backed jobs (e.g., using `asynq`), scales independently
  - **Redis**: Latest stable version, persisted volume for local convenience
  - **Reverse proxy** (Caddy or nginx): Routes requests, handles CORS headers, forwards `X-Forwarded-*` headers to the application, terminates TLS for staging/production testing

- **Development documentation** explaining:
  - How to point a local dev setup at a Supabase project (connection string setup)
  - How to populate `.env` locally
  - Which services are local (Docker) vs. remote (Supabase)
  - Port mappings and how to access each service

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

- **Reverse proxy configuration** (Caddy or nginx):
  - Correctly forwards `X-Forwarded-For`, `X-Forwarded-Proto`, `X-Forwarded-Host` to the Go application
  - Application trusts these headers from the proxy (configurable)
  - Handles cookie domain/path correctly when proxying

### Environment-Specific Profiles

- **Development**:
  - Relaxed CORS (or localhost-only)
  - Hot-reload enabled
  - Verbose logging
  - Postgres/Redis reachable locally via Docker Compose
  - Signing key rotation disabled for local testing

- **Staging**:
  - Full TLS (via reverse proxy)
  - Harder CORS (specific origins only)
  - Rate limiting enabled
  - Logging at info level
  - Integration with staging Supabase project

- **Production**:
  - TLS only
  - Strict CORS and security headers (CSP, HSTS, etc.)
  - Error messages sanitized (no stack traces to clients)
  - Structured logging only
  - Integration with production Supabase project
  - No hot-reload, no debug endpoints

### CI Pipeline

- **GitHub Actions workflow** (`.github/workflows/ci.yml`) running on every push and pull request:
  - **Go formatting**: `gofmt` or equivalent; fail if code is not formatted
  - **Linting**: `golangci-lint` (or similar) with strict rules
  - **Unit tests**: `go test ./...` with race detector enabled (`-race`); fail if coverage falls below a baseline (e.g., 50%)
  - **Migration validation**: Attempt to run the migration pipeline in a dry-run mode (e.g., against a test database schema) to catch SQL syntax errors
  - **Secret scanning**: Use a tool like `gitleaks` or `truffleHog` to detect accidentally committed secrets
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

- [ ] A new developer can start the full local stack with a single documented command (`docker compose up -d` or similar) after cloning the repo and populating `.env` from `.env.example`.

- [ ] The API container successfully connects to the configured Supabase Postgres instance, Redis, and loads all external provider credentials (bunny.net, Supabase Storage, Resend, Razorpay) via typed configuration validation, without errors.

- [ ] Production Docker images build successfully with no development secrets, no source code, and no build artifacts included; images can be scanned by a security scanner.

- [ ] The `/healthz` and `/readyz` endpoints correctly return `200 OK` when all dependencies are healthy, and `503 Service Unavailable` when Postgres or Redis is unreachable.

- [ ] `golang-migrate` successfully runs the initial sanity migration against a fresh Supabase database, creating the migration tracking table without error.

- [ ] The GitHub Actions CI pipeline runs on every push/PR, executing formatting checks, linting, unit tests, migration validation, and secret scanning; all checks pass with the scaffolded code.

- [ ] The reverse proxy correctly forwards `X-Forwarded-*` headers to the Go application, and CORS headers are configurable per environment without code changes.

- [ ] Structured JSON logging is active in production mode, including request IDs and durations; no sensitive configuration is logged.

## Commit convention

Your commit message MUST include `Closes #<issue-number>` (issue number to be filled in when published to GitHub) when the task's GitHub issue closes.
