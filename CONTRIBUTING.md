# Contributing

## Reporting bugs

Open a GitHub issue using the bug report template (available once the GitHub repository is live).

## Suggesting features

Check [FEATURE_MATRIX.md](FEATURE_MATRIX.md) first — many non-MVP ideas are already tracked there as deferred. For anything else, open a GitHub discussion or issue.

## Naming convention

"Growth LMS" is a placeholder working name (see [PRODUCT_BRIEF.md](PRODUCT_BRIEF.md)). It is stored as configuration, not hard-coded, so it can change without touching application code.

## Code style and conventions

Go/Gin backend, HTMX-driven server-rendered templates. Detailed style/lint conventions land with Task 2's infrastructure setup (`plans/lms-mvp/task-2-infrastructure.md`); this section will be filled in once that tooling exists.

## Pull request process

- One approval required before merge.
- CI must pass (formatting, vet/lint, tests, migration checks, secret scanning — see Task 2).
- When adding a new third-party dependency, update [LICENSES.md](LICENSES.md) with its name, version, and license. Do not commit dependencies with unknown or incompatible licenses without explicit team approval.

## Development setup

Go and local database setup instructions will be documented here once Task 2 (infrastructure) lands.
