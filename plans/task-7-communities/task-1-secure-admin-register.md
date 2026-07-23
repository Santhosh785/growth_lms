---
task: 1
name: secure-admin-register
parallel_group: 1
depends_on: []
issue: none
---

# Task 1: Secure the admin-register endpoint

## What to build

The `POST /api/auth/admin-register` endpoint (which creates verified users via
Supabase's Admin API, bypassing email confirmation and the signup rate limit)
must not be callable by anyone. Gate it behind the same platform-owner
authentication chain the platform admin dashboards use, and layer a per-IP
rate limit on top. Also stop the compiled server binary from being committed.

This is a standalone security fix that lands before the Task 7 feature work, so
the feature branch starts from a clean tree.

## Acceptance criteria

- [ ] `POST /api/auth/admin-register` requires an authenticated platform owner
      (rejects anonymous and non-platform-owner callers).
- [ ] The endpoint is additionally protected by a per-IP rate limit.
- [ ] The compiled server binary is gitignored and not tracked.
- [ ] Committed as its own change, separate from the Task 7 feature commits.

## Commit convention

Standalone security fix; no plan issue to close (disk-only plan).
