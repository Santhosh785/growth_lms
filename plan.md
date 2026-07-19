# Own Learning Platform Roadmap

## Goal

Build an independent, production-ready online learning platform with the major capabilities found in LearnHouse, but under your own product name, domain, branding, infrastructure, and business rules.

The first release should support creators and organizations that create courses, publish lessons, enroll learners, sell access, and measure progress. Advanced capabilities should be added only after the core learning and commerce flows are reliable.

This roadmap is a product and engineering plan. It does not copy LearnHouse branding or assume that LearnHouse-specific services, enterprise modules, or credentials are available.

## Product scope

### Roles

- Platform owner: manages the whole installation, plans, system settings, and support operations.
- Organization owner: manages one school, academy, or business workspace.
- Teacher/creator: creates courses, lessons, assignments, quizzes, and announcements.
- Learner: discovers courses, purchases or receives access, learns, submits work, and earns certificates.
- Moderator: manages discussions and reported content without full organization administration.

### Core product areas

1. Authentication and account management.
2. Organizations, custom branding, domains, memberships, and role-based access.
3. Course authoring with a block-based editor.
4. Student course player and progress tracking.
5. Assessments, assignments, grading, certificates, and completion rules.
6. Course catalog, collections, offers, checkout, payments, refunds, and entitlements.
7. Communities, discussions, notifications, and email.
8. Media, podcasts, documents, SCORM, and content delivery.
9. Analytics for learners, creators, organizations, and platform operators.
10. AI learning tools, interactive playgrounds, code exercises, and real-time boards.
11. SEO, landing pages, themes, embeds, and public course pages.
12. Deployment, backups, observability, security, testing, and administration.

```

## Stack
- Backend: Golang (net/http / Gin / Fiber)
- Frontend: HTML + HTMX + TailwindCSS
- Database: Supabase PostgreSQL
- Auth: Supabase Auth (JWT verified in Go)
- Storage: Supabase Storage
- Payments: Razorpay / Stripe

## Phases and tasks

| # | Task | Phase | Depends on | Status |
|---|---|---|---|---|
| 1 | Product identity, repository, licensing, and requirements | Foundation | None | pending |
| 2 | Local infrastructure, configuration, and deployment foundation | Foundation | 1 | pending |
| 3 | Authentication, organizations, tenancy, and permissions | Core platform | 2 | pending |
| 4 | Course domain, media, authoring, and publishing | Learning core | 3 | pending |
| 5 | Learner portal, player, progress, assessments, and certificates | Learning core | 4 | pending |
| 6 | Catalog, offers, checkout, payments, and entitlements | Commerce | 3, 4 | pending |
| 7 | Communities, notifications, email, and collaboration | Engagement | 3, 4, 5 | pending |
| 8 | Analytics, search, SEO, themes, and public pages | Platform growth | 4, 5, 6 | pending |
| 9 | AI, playgrounds, code execution, podcasts, SCORM, and boards | Advanced features | 5, 7, 8 | pending |
| 10 | Admin console, CLI, backups, observability, and operations | Operations | 2, 3, 6 | pending |
| 11 | Security, automated testing, performance, and accessibility | Hardening | 3-10 | pending |
| 12 | Production launch, migration, documentation, and future mobile app | Launch | 11 | pending |

## Detailed roadmap

### Task 1: Product identity, repository, licensing, and requirements

Define your product name, domain, visual identity, target customer, pricing model, supported countries, payment providers, and first-release scope. Create a new repository and write your own README, contribution guide, security policy, privacy policy outline, and data-retention policy.

If any code is copied from an existing open-source project, preserve its copyright and license notices and complete a license review before commercial launch. Do not reuse LearnHouse names, logos, URLs, screenshots, enterprise code, or credentials.

Deliverables:

- Product brief.
- Feature priority matrix.
- New repository identity.
- Dependency and license inventory.
- Initial domain model.
- Definition of done for the MVP.

### Task 2: Local infrastructure, configuration, and deployment foundation

Create development and production configuration for the web app, API, PostgreSQL, Redis, object storage, worker, reverse proxy, and optional collaboration service.

Implement:

- `.env.example` files with your own variable prefix.
- Typed configuration validation.
- Docker Compose development stack.
- Database migration commands.
- Health and readiness endpoints.
- Structured logging.
- CORS, trusted origins, cookie, and proxy configuration.
- Separate development, staging, and production settings.
- CI checks for formatting, type checking, tests, migrations, and secret scanning.

Acceptance criteria:

- A new developer can start the stack from documented commands.
- The API can connect to PostgreSQL, Redis, and storage.
- Production images build without development secrets.
- Health checks detect unavailable dependencies.

### Task 3: Authentication, organizations, tenancy, and permissions

Build the security foundation before business features.

Implement:

- Email/password registration and login.
- Email verification, password reset, sessions, logout, and account deletion.
- OAuth providers such as Google only after the password flow is stable.
- Optional MFA.
- Users, organizations, memberships, invitations, roles, and permissions.
- Platform owner, organization owner, teacher, moderator, and learner roles.
- Organization isolation on every organization-owned table.
- Audit events for security and administrative actions.
- API tokens for integrations.
- Rate limiting and abuse protection.

Acceptance criteria:

- A user cannot read or mutate another organization’s data.
- Role permissions are enforced server-side, not only in the UI.
- Organization invitation and removal flows work.
- Tenant-isolation tests pass for every major resource.

### Task 4: Course domain, media, authoring, and publishing

Build the creator experience.

Database entities should include courses, chapters, lessons, blocks, assets, categories, tags, versions, publication status, prerequisites, and completion rules.

Implement:

- Course create/edit/delete/archive.
- Draft, review, scheduled, published, and unpublished states.
- Course cover images and metadata.
- Chapters and lesson ordering.
- Block-based content editor for text, image, video, audio, files, embeds, callouts, code, quizzes, and downloads.
- Autosave and explicit publish operation.
- Course preview mode.
- Media upload through signed storage URLs.
- Image and video metadata.
- Access control for private media.
- Course duplication and version history.
- Collections and bundles.
- Course import/export foundation.

Acceptance criteria:

- A teacher can create and publish a complete course without database access.
- Draft content is not visible to learners.
- Uploaded files are stored outside the application container.
- Course ordering and block content survive reloads and concurrent edits.

### Task 5: Learner portal, player, progress, assessments, and certificates

Build the complete learning journey.

Implement:

- Public course pages and authenticated enrollment pages.
- Course player with navigation and resume position.
- Lesson completion and progress percentages.
- Video progress and watch thresholds.
- Course prerequisites.
- Quizzes with question banks, attempts, scoring, and passing grades.
- Assignments with file submission, due dates, grading, feedback, and resubmission.
- Learner dashboard and continue-learning view.
- Course announcements.
- Completion rules.
- Certificate templates, issuance, verification URLs, and downloadable PDFs.
- Learner notifications and email reminders.

Acceptance criteria:

- Learners can resume a course accurately across devices.
- Teachers can review, grade, and return assignments.
- Certificates are issued only when completion rules pass.
- Learner APIs never expose answer keys or teacher-only data.

### Task 6: Catalog, offers, checkout, payments, and entitlements

Build commerce as a separate domain from course content.

Implement:

- Free, paid, subscription, cohort, and invitation-only offers.
- Prices, currencies, tax fields, discount codes, coupons, and availability windows.
- Server-created checkout orders.
- Payment-provider adapter interface.
- Start with one provider relevant to your market; add Stripe/Razorpay later through adapters.
- Signed webhook verification.
- Idempotent payment-event handling.
- Purchase, payment, refund, chargeback, and entitlement records.
- Enrollment only after verified payment or explicit admin grant.
- Refund and access-revocation policies.
- Creator revenue reporting.
- Payment audit trail.

Acceptance criteria:

- The browser return URL cannot grant access by itself.
- Duplicate webhooks do not duplicate enrollment or revenue.
- Failed, refunded, and disputed payments have explicit states.
- Payment secrets never reach the browser or repository.

### Task 7: Communities, notifications, email, and collaboration

Implement engagement features after the core learning flow works.

Implement:

- Organization and course discussions.
- Posts, replies, reactions, mentions, moderation, reports, and pinning.
- Direct and broadcast notifications.
- Email templates and provider abstraction.
- Notification preferences and unsubscribe controls.
- Real-time presence and collaboration service.
- Shared boards and collaborative documents using WebSockets/Yjs.
- Internal service authentication between API and collaboration server.

Optional later features include live classes, chat rooms, community gamification, and WhatsApp/SMS notifications.

### Task 8: Analytics, search, SEO, themes, and public pages

Make the platform discoverable and measurable.

Implement:

- Events for course views, enrollments, lesson starts, completions, searches, purchases, refunds, and certificates.
- Creator analytics for enrollment, completion, revenue, learner activity, and drop-off.
- Organization analytics and platform reports.
- Background event aggregation.
- Search across courses, lessons, users, and discussions.
- Sitemaps, robots configuration, Open Graph metadata, and structured data.
- Landing-page builder or configurable public pages.
- Theme tokens for colors, typography, spacing, and components.
- Custom logo, favicon, email branding, and organization settings.
- Custom domains with domain verification.
- Embeddable course catalog and checkout links.

### Task 9: Advanced learning features

Add independently deployable modules for:

- AI course outlines, lesson drafting, quiz generation, and course-scoped tutors.
- AI usage limits, cost tracking, provider abstraction, and prompt/version logging.
- Interactive simulations and diagrams.
- Sandboxed code execution with CPU, memory, time, network, and filesystem limits.
- Podcast episodes, playlists, RSS, transcripts, and progress.
- SCORM 1.2/2004 package validation, launch, progress, and reporting.
- Improved collaborative boards.

Advanced modules must be feature-flagged, tenant-scoped, observable, and independently testable.

### Task 10: Admin console, CLI, backups, and operations

Implement:

- Platform admin dashboard.
- User, organization, course, payment, and moderation administration.
- Feature flags and plan limits.
- Usage and quota management.
- API token management.
- Background job dashboard.
- Database and media backup policy.
- CLI commands for setup, dev, start, stop, logs, status, health, backup, restore, and migration.
- Upgrade and migration process.
- Error tracking, metrics, traces, request IDs, and audit logs.
- Alerts for failed jobs, payment webhooks, storage, database, and authentication errors.

### Task 11: Security, testing, performance, and accessibility

Add unit, integration, database, permission, payment, frontend, collaboration, security, and end-to-end tests.

Release gates must cover:

- Secure cookies, CSRF, CORS, trusted origins, and rate limits.
- Server-side authorization for every protected operation.
- Upload MIME, size, archive, and malware validation.
- XSS, SQL injection, dependency, and container scanning.
- Secret management and rotation.
- Database indexes, pagination, caching, CDN media, and background jobs.
- Load tests for catalog, player, checkout, and webhook endpoints.
- Keyboard navigation, screen readers, focus management, contrast, captions, and accessible forms.

### Task 12: Production launch, migration, documentation, and mobile

Prepare staging and production, TLS, payment webhooks, backups, security review, privacy and terms pages, support procedures, monitoring, pilot onboarding, migration documentation, and developer documentation.

Mobile comes after the web product stabilizes:

- Start with a responsive web experience/PWA.
- Reuse the REST API.
- Add one Expo/React Native application.
- Support runtime organization branding before separate app-store builds.

## Execution phases

### Phase A: Foundation

Tasks 1-3: repository, development stack, authentication, organization model, and permissions.

### Phase B: MVP learning product

Tasks 4-6: course authoring, learner experience, progress, assessments, certificates, offers, checkout, payments, and entitlements.

### Phase C: Engagement and growth

Tasks 7-8: communities, notifications, collaboration, analytics, search, public pages, themes, and custom domains.

### Phase D: Advanced platform

Tasks 9-10: AI, code, playgrounds, podcasts, SCORM, administration, CLI, backups, and operations.

### Phase E: Release hardening

Tasks 11-12: security, testing, performance, accessibility, staging, pilot launch, documentation, and mobile planning.

## Recommended MVP boundary

The first usable product should include only Tasks 1-6:

- One organization per installation initially, with the schema ready for multi-tenancy.
- Email authentication.
- Teacher and learner roles.
- Course and lesson creation.
- Video, text, files, and quizzes.
- Learner progress.
- Free and paid enrollment.
- One payment provider.
- Basic certificates.
- Basic email notifications.
- Basic admin page.

Do not block the MVP on AI, live classes, mobile apps, code execution, SCORM, advanced analytics, or complex multi-organization domains.

## Definition of done

The platform is ready for a controlled production pilot when:

- A new organization can be created without developer intervention.
- A teacher can publish and sell a course.
- A learner can register, pay, learn, submit work, and receive a certificate.
- Payment access is granted only after verified provider events.
- Tenant isolation and authorization tests pass.
- Backups can be restored successfully.
- Critical user journeys have end-to-end tests.
- Monitoring, error tracking, logs, and alerts are active.
- The product uses your own branding, domains, accounts, policies, and documentation.
- License and third-party dependency obligations have been reviewed.
