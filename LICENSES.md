# Third-Party License Inventory

This document tracks the licenses of third-party dependencies used inside the Growth LMS codebase. It is unrelated to the product's own license, which is Proprietary / All Rights Reserved (see [README.md](README.md)) — this inventory only concerns *dependencies*, not the product itself.

## Go dependencies

None yet — this section will be populated from `go.mod`/`go.sum` as Task 2 and later tasks add dependencies.

## Node/npm dependencies (if any frontend build tooling is used)

None yet.

## License policy

Compatible licenses include MIT, Apache 2.0, BSD, ISC, and MPL 2.0. Licenses that require source disclosure (GPL, AGPL) require review and approval before merge.

**Process:** before adding a new dependency, audit its license. Note the license in the PR description or a linked issue. If the license is unknown or incompatible, consult the team lead before merging.

**Future improvement (post-MVP):** integrate a license-audit tool (e.g. `go-licenses` or `license-report`) into CI to catch unlicensed or incompatible dependencies automatically.

## Third-party code / content

No code or content has been copied from other learning platforms. If this changes, the source's copyright and license notices must be preserved and a license review completed before commercial launch.
