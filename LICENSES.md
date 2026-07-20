# Third-Party License Inventory

This document tracks the licenses of third-party dependencies used inside the Growth LMS codebase. It is unrelated to the product's own license, which is Proprietary / All Rights Reserved (see [README.md](README.md)) — this inventory only concerns *dependencies*, not the product itself.

## Go dependencies

Direct dependencies introduced in Task 2 (infrastructure scaffolding); transitive dependencies are pulled in automatically and tracked in `go.sum`.

| Package | Purpose | License |
|---|---|---|
| [gin-gonic/gin](https://github.com/gin-gonic/gin) | HTTP router/framework | MIT |
| [gin-contrib/cors](https://github.com/gin-contrib/cors) | CORS middleware for Gin | MIT |
| [google/uuid](https://github.com/google/uuid) | Request ID generation | BSD-3-Clause |
| [hibiken/asynq](https://github.com/hibiken/asynq) | Redis-backed background job queue | MIT |
| [jackc/pgx](https://github.com/jackc/pgx) | Postgres driver/connection pool | MIT |
| [joho/godotenv](https://github.com/joho/godotenv) | `.env` file loading | MIT |
| [redis/go-redis](https://github.com/redis/go-redis) | Redis client | BSD-2-Clause |

All licenses above are pre-approved under the license policy below (MIT/BSD).

## Node/npm dependencies (if any frontend build tooling is used)

None yet.

## License policy

Compatible licenses include MIT, Apache 2.0, BSD, ISC, and MPL 2.0. Licenses that require source disclosure (GPL, AGPL) require review and approval before merge.

**Process:** before adding a new dependency, audit its license. Note the license in the PR description or a linked issue. If the license is unknown or incompatible, consult the team lead before merging.

**Future improvement (post-MVP):** integrate a license-audit tool (e.g. `go-licenses` or `license-report`) into CI to catch unlicensed or incompatible dependencies automatically.

## Third-party code / content

No code or content has been copied from other learning platforms. If this changes, the source's copyright and license notices must be preserved and a license review completed before commercial launch.
