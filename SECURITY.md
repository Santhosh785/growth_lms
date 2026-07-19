# Security Policy

## Reporting a vulnerability

Please report security issues privately to `security@<product-domain-tbd>` (a real address will replace this placeholder once the product's operating domain is finalized — see [PRODUCT_BRIEF.md](PRODUCT_BRIEF.md)).

Do not publicly disclose a security issue (e.g. via a public GitHub issue) before it has been reported privately and addressed.

## Commit message convention

Security-related fixes follow the same convention as all commits: include `Closes #<issue-number>` to link the fix to its tracking issue.

## Known security considerations (deferred)

- Authentication, authorization, and tenant isolation are addressed in Task 3 (`plans/lms-mvp/task-3-auth-tenancy.md`) — tenant isolation is enforced via Postgres Row-Level Security, not application code alone.
- Infrastructure hardening (secrets management, CORS, rate limiting foundations) is addressed in Task 2 (`plans/lms-mvp/task-2-infrastructure.md`).
- Payment security (webhook signature verification, secret handling) is addressed in Task 6 (`plans/lms-mvp/task-6-commerce.md`).

This document will be expanded with a full disclosure policy and supported-versions table as those tasks land.
