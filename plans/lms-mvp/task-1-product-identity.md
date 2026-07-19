---
task: 1
name: product-identity
parallel_group: 1
depends_on: []
issue: TBD
---

# Task 1: Product Identity, Repository Setup, and Domain Model

## What to build

### 1. Product Brief

Write a product brief document (PRODUCT_BRIEF.md) that defines:

- **Product model**: A hosted, multi-tenant SaaS platform operated by the platform owner. Multiple customer organizations sign up and run their own school/academy workspace on shared infrastructure — this is not self-hosted software distributed to customers.
- **Target customer**: Educational teams, academies, and businesses that want to sell and run courses without operating their own infrastructure.
- **Problem solved**: Educators need a learning platform they can start using immediately (self-service signup) without vendor lock-in on content, complex enterprise pricing, or infrastructure to manage themselves.
- **First-release scope (MVP boundary)**: Public multi-tenant SaaS from launch (any customer can sign up and create their own organization), email authentication, teacher and learner roles (plus platform owner, organization owner, and moderator per the full role model), course and lesson authoring with text/image/video/file/quiz content, learner progress tracking, free and paid course enrollment via Razorpay, basic certificate generation (server-side PDF), basic email notifications, and a basic admin dashboard.
- **Pricing model direction**: The platform monetizes via a commission on course sales (a percentage/fixed fee taken from each learner payment, built in Task 6) — organizations are not charged a separate subscription fee to use the platform in the MVP. Exact commission rate is a business decision to be set as configuration, not hardcoded.
- **Success metric for MVP**: A new organization can sign up unassisted, a teacher in that org can create a course, invite learners, deliver content, track progress, and issue certificates — with zero cross-tenant data visibility between organizations.

### 2. Feature Priority Matrix

Create a FEATURE_MATRIX.md document that explicitly categorizes:

- **In MVP (Tasks 1–6 deliverables)**:
  - Public multi-tenant SaaS: any authenticated user can create a new organization (self-service signup), with full data isolation between organizations (enforced via Postgres Row-Level Security, not just application code)
  - Email/password authentication
  - Full role model: platform owner, organization owner, teacher/creator, moderator, learner
  - Course creation and management (title, description, image, free/paid flag)
  - Lesson creation and reordering
  - Content blocks: text, images, video (first-party hosted via bunny.net, with signed URLs), file attachments, multiple-choice quizzes
  - Learner progress (lesson view/completion, quiz scores)
  - Free and paid enrollment via Razorpay (single payment provider for MVP)
  - Certificate generation: server-side-generated PDF, downloadable, with a public verification URL
  - Email notifications (new course assigned, new lesson published, certificate earned) sent via Resend
  - Basic admin dashboard (user list, course list, enrollment overview) scoped per organization, plus a platform-owner view across organizations — built in Task 6, once organization, course, and payment data all exist

- **Explicitly deferred (no code, no placeholder; acknowledge in matrix)**:
  - AI-powered tools (auto-grading, content suggestions, chat tutors)
  - Live classes and real-time video conferencing
  - Mobile apps (iOS, Android)
  - Code execution environments
  - SCORM compliance and xAPI tracking
  - Advanced analytics and learning science metrics
  - Community features (forums, peer discussion)
  - White-labeling / custom domains per organization
  - SSO, LDAP, or third-party authentication (Google OAuth and MFA are deferred past MVP; email/password only for now)
  - Real-time collaboration (co-authoring lessons)
  - Advanced grading rubrics and manual grading workflows

### 3. Repository Identity

Initialize or establish the repository with:

- **Git setup** (if not already done):
  - Run `git init` in the repository root
  - Create an initial commit with these foundational documents

- **Placeholder product name**:
  - Use a working name (e.g., "Growth LMS" or similar) that can be changed later in branding/marketing without code dependencies
  - Store as a configuration value or environment variable, not hard-coded in code
  - Document the naming convention in CONTRIBUTING.md

- **README.md**:
  - One-paragraph product description and target use case (multi-tenant SaaS learning platform)
  - Link to PRODUCT_BRIEF.md for detailed scope
  - Quick-start development instructions (at high level; Task 2 will detail deployment)
  - Link to CONTRIBUTING.md and SECURITY.md
  - License: **Proprietary / All Rights Reserved**. State plainly that this is closed-source, privately operated SaaS — no OSS license applies. (Revisit only if a future decision is made to open-source specific components.)

- **CONTRIBUTING.md**:
  - How to report bugs (link to GitHub issues template once GitHub repo is live)
  - How to suggest features (defer non-MVP items to the feature matrix; link to discussions or issues)
  - Code style and conventions (Go/Gin best practices; HTMX patterns; naming; TBD specifics per Task 2)
  - PR review process (one approval before merge; CI must pass)
  - Development setup (link to Task 2 once available; mention Go, Node.js versions, local DB setup TBD)

- **SECURITY.md**:
  - Security reporting: a neutral placeholder security contact (e.g. `security@<product-domain-tbd>`) or link to a Responsible Disclosure Policy document — do not use a real operating company domain here, since the product's own domain is still TBD per Task 1's product-identity placeholder decision
  - Do not publicly disclose security issues; report privately to security contact
  - Commit message convention: `Closes #<issue>` for GitHub issue tracking
  - Known security considerations deferred (Task 3 addresses auth/authz/tenant isolation; Task 2 addresses infrastructure)

- **PRIVACY.md** (draft/placeholder):
  - Data collected: user name, email, progress data, quiz responses, certificates
  - Data retention: learners can request export or deletion (process TBD; post-MVP)
  - Third parties: list Supabase (database/auth), bunny.net (video), Resend (email), Razorpay (payments)
  - Statement: "This document is a draft. Legal review and compliance work will be completed before production launch."

- **DATA_RETENTION.md** (draft/placeholder):
  - Learners: data retained while enrolled; archived x days after unenrollment (x TBD, e.g., 90 days for export; then deleted)
  - Teachers: data retained indefinitely while teacher/admin status active; deleted on request
  - Deleted data: backups may retain for y days (y TBD, e.g., 30 days)
  - Compliance: GDPR right-to-deletion supported (implementation deferred; post-MVP)
  - Statement: "This document is a draft. Legal review will be completed before production launch."

### 4. Dependency and License Inventory Convention

Establish a process for tracking third-party licenses and dependencies:

- **Create LICENSES.md or NOTICE file** that includes:
  - A section listing all direct Go dependencies (from go.mod/go.sum), their versions, and licenses (to be populated as Task 2+ adds code)
  - A section listing all npm/node dependencies (if any frontend build tooling is used), their versions, and licenses
  - A statement: "Compatible licenses include MIT, Apache 2.0, BSD, ISC, and MPL 2.0. Licenses that require source disclosure (GPL, AGPL) require review and approval before merge." (Note: this governs third-party *dependencies* used inside the proprietary codebase — it is unrelated to the product's own license, which is Proprietary / All Rights Reserved per the README decision above.)
  - A process: "Before adding a new dependency, audit its license. Add a GitHub comment or issue note with the license. If the license is unknown or incompatible, consult the team lead."

- **Document in CONTRIBUTING.md**:
  - Add a checklist item: "When adding a new third-party dependency, update LICENSES.md with the dependency name, version, and license."
  - Do not commit dependencies with unknown or proprietary licenses without explicit team approval.

- **Automation (optional, post-MVP)**:
  - Note in README or CONTRIBUTING.md: "Future improvement: integrate a license-audit tool (e.g., go-licenses or license-report) in CI to catch unlicensed dependencies automatically."

### 5. Initial Domain Model

Write DOMAIN_MODEL.md or include a domain model section in the architecture documentation. This is a **conceptual sketch** — entities and how they relate — not a database schema. Exact fields, types, and constraints are owned by the tasks that actually build each area (Task 3 for identity/organizations, Task 4 for course content, Task 5 for learner progress/certificates, Task 6 for commerce) and may reasonably differ in detail from this sketch; do not treat this document as binding schema.

**Core Entities:**

- **Organization**: A customer's workspace/tenant on the platform. Has many: Memberships (linking Users with a role), Courses.
- **User**: A person account, authenticated via Supabase Auth. May be a platform owner (a global flag/role, not tied to a specific organization) and/or hold a membership (with a role) in one or more organizations.
- **Membership**: Links a User to an Organization with a role. Role is one of: organization owner, teacher/creator, moderator, learner. (Platform owner is a separate, global distinction from these per-organization roles — see User above.)
- **Course**: Belongs to an Organization. Has many: Chapters, Offers, Purchases.
- **Chapter**: Groups Lessons within a Course, in order.
- **Lesson**: Belongs to a Chapter. Has many: Blocks, and Progress records (one per learner).
- **Block**: A single piece of content within a Lesson (text, image, video, file, or quiz). Quiz blocks store their questions/answers server-side only.
- **Offer**: A pricing variant of a Course (free or paid). Has many: Purchases.
- **Purchase**: A learner's enrollment/payment record against a Course via an Offer. Has one: Entitlement.
- **Entitlement**: Derived from a Purchase; the actual access grant used to decide whether a learner may view a Course.
- **Progress**: Tracks a learner's completion status and quiz results per Lesson.
- **Certificate**: Issued to a learner upon Course completion; carries a public verification identifier.
- **Notification**: A record of an email sent to a User (course assigned, lesson published, certificate earned, etc.), optionally tied to a Course.

**Relationships Summary (ER-style notation):**

```
Organization
  |-- has many Users (through Membership)
  |-- has many Courses
  |-- has many Memberships

User
  |-- has many Memberships
  |-- may be platform owner (global, independent of any Membership)
  |-- has many Progress records (as learner)
  |-- has many Certificates (as learner)
  |-- has many Courses (as teacher/creator, via Membership role)
  |-- has many Notifications

Course
  |-- belongs to Organization
  |-- has many Chapters
  |-- has many Offers
  |-- has many Purchases
  |-- has many Entitlements
  |-- has many Certificates

Chapter
  |-- belongs to Course
  |-- has many Lessons

Lesson
  |-- belongs to Chapter
  |-- has many Blocks
  |-- has many Progress (one per learner)

Block
  |-- belongs to Lesson

Offer
  |-- belongs to Course
  |-- has many Purchases

Purchase
  |-- belongs to Course
  |-- belongs to User (learner)
  |-- belongs to Offer
  |-- has one Entitlement

Entitlement
  |-- belongs to Purchase
  |-- belongs to Course
  |-- belongs to User (learner)

Progress
  |-- belongs to Lesson
  |-- belongs to User (learner)

Certificate
  |-- belongs to Course
  |-- belongs to User (learner)

Notification
  |-- belongs to User
  |-- belongs to Course (nullable)

Membership
  |-- belongs to User
  |-- belongs to Organization
```

### 6. Definition of Done for MVP

Create a DEFINITION_OF_DONE_MVP.md file:

The MVP is production-ready and launch-eligible when:

1. **Product scope (Task 1)**: Product brief, feature matrix, domain model, and repository identity are documented and agreed.
2. **Infrastructure and tooling (Task 2)**: Go/Gin API scaffold, HTMX frontend structure, Docker Compose for local development, and basic CI/CD pipeline are in place and tested.
3. **Authentication, organizations, and tenancy (Task 3)**: Email/password auth, the full role model (platform owner, org owner, teacher, moderator, learner), public multi-tenant organization self-signup, and Row-Level-Security-enforced tenant isolation are working end-to-end; security-relevant tests pass.
4. **Course domain (Task 4)**: Supabase/PostgreSQL schema for courses/chapters/lessons/blocks is implemented and matches the domain model's intent; migrations are reversible and versioned via golang-migrate.
5. **Learner journey and commerce (Tasks 5–6)**: All features in the MVP boundary are implemented and tested:
   - Courses and lessons can be created, edited, and deleted (by teachers and org owners)
   - Content blocks (text, images, video, files, quizzes) render correctly
   - Learners can enroll (free and paid, via Razorpay)
   - Progress and quiz scores are tracked
   - Certificates are generated server-side as PDFs and downloadable
   - Email notifications are sent
   - Admin dashboard shows basic reports, per-organization and platform-wide
6. **Quality gates**:
   - All acceptance criteria for Tasks 1–6 are verified and closed
   - Code review: all changes merged via PR with at least one approval and passing CI
   - Test coverage: critical paths (auth, tenant isolation, enrollment, progress, payment) have automated tests (unit or integration)
   - Security: SECURITY.md is complete; no known high-severity vulnerabilities; secrets are not committed
   - Documentation: README, CONTRIBUTING, API endpoints, and database schema are documented and current
7. **Deployment readiness**:
   - A staging deployment on the single-VPS Docker Compose target (per Task 2) is live and reachable
   - Environment variables and secrets are managed via .env files (not committed) or a secrets service
   - Database backups are tested
   - Health check and error logging are in place
8. **Legal and compliance (placeholders)**:
   - PRIVACY.md and DATA_RETENTION.md exist and describe current handling (marked as draft)
   - LICENSES.md documents all third-party dependencies and licenses
   - No code or content is copied from other learning platforms without license preservation and attribution
9. **Launch readiness**:
   - A public GitHub repository (or similar) is created with all documentation
   - A basic landing page or "about" section describes the product
   - Contributors and users know how to report issues and contribute
   - A post-launch roadmap is drafted (for features deferred beyond MVP)

## Acceptance criteria

- [ ] Product brief document (PRODUCT_BRIEF.md) exists and clearly defines the multi-tenant SaaS model, target customer, MVP scope, and pricing model direction
- [ ] Feature priority matrix (FEATURE_MATRIX.md) exists and explicitly lists MVP-included features (including public multi-tenant self-signup) and deferred features
- [ ] Repository is initialized with git and contains a placeholder product name
- [ ] README.md exists, describes the product and development setup, states the Proprietary / All Rights Reserved license, and links to PRODUCT_BRIEF.md and CONTRIBUTING.md
- [ ] CONTRIBUTING.md exists and outlines code standards, PR process, and how to report issues
- [ ] SECURITY.md exists, uses a neutral placeholder security contact (not a real operating domain), and defines responsible disclosure
- [ ] PRIVACY.md (draft) exists and outlines data collection, retention, and third-party processors (Supabase, bunny.net, Resend, Razorpay)
- [ ] DATA_RETENTION.md (draft) exists and outlines data retention windows and deletion policies
- [ ] LICENSES.md or NOTICE file exists and documents the license audit process for third-party dependencies (distinct from the product's own proprietary license)
- [ ] Initial domain model (DOMAIN_MODEL.md or equivalent) is written at the entity + relationship level only (no prescribed fields/types) and includes all entities: Organization, User, Membership, Course, Chapter, Lesson, Block, Offer, Purchase, Entitlement, Progress, Certificate, Notification
- [ ] Domain model includes a relationship diagram or ER notation showing how entities connect, and reflects the full role model (platform owner, organization owner, teacher, moderator, learner)
- [ ] Definition of Done for MVP (DEFINITION_OF_DONE_MVP.md) is written, uses the correct task numbers (2=infra, 3=auth/tenancy, 4=course domain, 5=learner journey, 6=commerce), and maps to acceptance criteria of Tasks 1–6
- [ ] All foundational documents are committed to git with clear, atomic commit messages

## Commit convention

Your commit message MUST include `Closes #<issue-number>` (issue number to be filled in when published to GitHub) when the task's GitHub issue closes.
