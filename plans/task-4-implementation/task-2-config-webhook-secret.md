---
task: 2
name: config-webhook-secret
parallel_group: 1
depends_on: []
issue: N/A (disk-only draft, no GitHub remote configured)
---

# Task 2: Bunny webhook secret config

## What to build

Extend `internal/config/config.go`:
- Add `WebhookSecret string` field to `BunnyNetConfig`.
- Add `LMS_BUNNY_WEBHOOK_SECRET` to `Load()`'s `required` slice (no custom
  validator needed, same as `LMS_BUNNY_API_KEY`), and wire it into the
  `BunnyNetConfig` literal built at the end of `Load()`.
- Do not add it to `Redacted()`'s output map (it's a secret, like
  `LMS_BUNNY_API_KEY` is already omitted there).
- Update `.env.example` with the new required variable.

## Acceptance criteria

- [ ] `config.Load()` fails fast with a clear error if
      `LMS_BUNNY_WEBHOOK_SECRET` is unset, matching the existing error
      message format (`"%s is required"`).
- [ ] `internal/config/config_test.go` extended (or a new test added) to
      cover the new required var, following that file's existing table-test
      pattern.
- [ ] `.env.example` documents the new variable.

## Commit convention

This is a disk-only plan (no GitHub remote configured) — commit normally
with a descriptive message; no `Closes #` trailer applies.
