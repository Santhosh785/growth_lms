# Definition of Done — MVP

The MVP is production-ready and launch-eligible when:

1. **Product scope (Task 1)** — Product brief, feature matrix, domain model, and repository identity are documented and agreed.
2. **Infrastructure and tooling (Task 2)** — Go/Gin API scaffold, HTMX frontend structure, Docker Compose for local development, and a basic CI/CD pipeline are in place and tested.
3. **Authentication, organizations, and tenancy (Task 3)** — Email/password auth, the full role model (platform owner, org owner, teacher, moderator, learner), public multi-tenant organization self-signup, and Row-Level-Security-enforced tenant isolation are working end-to-end; security-relevant tests pass.
4. **Course domain (Task 4)** — Supabase/PostgreSQL schema for courses/chapters/lessons/blocks is implemented and matches the domain model's intent; migrations are reversible and versioned via golang-migrate.
5. **Learner journey and commerce (Tasks 5–6)** — All features in the MVP boundary are implemented and tested:
   - Courses and lessons can be created, edited, and deleted (by teachers and org owners).
   - Content blocks (text, images, video, files, quizzes) render correctly.
   - Learners can enroll (free and paid, via Razorpay).
   - Progress and quiz scores are tracked.
   - Certificates are generated server-side as PDFs and downloadable.
   - Email notifications are sent.
   - Admin dashboard shows basic reports, per-organization and platform-wide (Task 6).
6. **Quality gates**:
   - All acceptance criteria for Tasks 1–6 are verified and closed.
   - Code review: all changes merged via PR with at least one approval and passing CI.
   - Test coverage: critical paths (auth, tenant isolation, enrollment, progress, payment) have automated tests (unit or integration).
   - Security: SECURITY.md is complete; no known high-severity vulnerabilities; secrets are not committed.
   - Documentation: README, CONTRIBUTING, API endpoints, and database schema are documented and current.
7. **Deployment readiness**:
   - A staging deployment on the single-VPS Docker Compose target (per Task 2) is live and reachable.
   - Environment variables and secrets are managed via .env files (not committed) or a secrets service.
   - Database backups are tested.
   - Health check and error logging are in place.
8. **Legal and compliance (placeholders)**:
   - PRIVACY.md and DATA_RETENTION.md exist and describe current handling (marked as draft).
   - LICENSES.md documents all third-party dependencies and licenses.
   - No code or content is copied from other learning platforms without license preservation and attribution.
9. **Launch readiness**:
   - A public GitHub repository (or similar) is created with all documentation.
   - A basic landing page or "about" section describes the product.
   - Contributors and users know how to report issues and contribute.
   - A post-launch roadmap is drafted (for features deferred beyond MVP).
