# Growth LMS

*(Working name — final product name/branding is deferred; see [PRODUCT_BRIEF.md](PRODUCT_BRIEF.md).)*

Growth LMS is a hosted, multi-tenant SaaS learning platform: organizations sign up, author and sell courses, and manage learners, all on shared infrastructure operated by the platform owner. See [PRODUCT_BRIEF.md](PRODUCT_BRIEF.md) for the full product scope and [FEATURE_MATRIX.md](FEATURE_MATRIX.md) for what's in and out of the MVP.

## Development setup

See [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for the full local setup: installing the Supabase CLI, populating `.env`, running migrations, and starting the Docker Compose stack (API, worker, Redis, nginx).

Stack: Go (Gin) backend serving both server-rendered HTML (HTMX + Tailwind CSS) and a JSON API, Supabase (Postgres + Auth), bunny.net for video, Redis-backed background jobs (asynq), Resend for email, Razorpay for payments.

Quick start:

```bash
supabase start          # local Postgres/Auth/Storage
cp .env.example .env    # fill in values printed by `supabase start` + sandbox creds
make migrate-up
docker compose up -d    # API, worker, Redis, nginx
curl http://localhost:8080/healthz
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for code standards, the PR process, and how to report issues or suggest features.

## Security

See [SECURITY.md](SECURITY.md) for how to report a security issue.

## License

Proprietary / All Rights Reserved. This is closed-source, privately operated SaaS — no open-source license applies to the product itself. See [LICENSES.md](LICENSES.md) for the license inventory of third-party dependencies used within the codebase.
