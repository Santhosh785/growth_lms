# Feature Priority Matrix

See [PRODUCT_BRIEF.md](PRODUCT_BRIEF.md) for the product model and target customer, and [plans/lms-mvp/main-plan.md](plans/lms-mvp/main-plan.md) for the full task breakdown and decision log.

## In MVP (Tasks 1–6 deliverables)

- Public multi-tenant SaaS: any authenticated user can create a new organization (self-service signup), with full data isolation between organizations enforced via Postgres Row-Level Security, not just application code.
- Email/password authentication.
- Full role model: platform owner, organization owner, teacher/creator, moderator, learner.
- Course creation and management (title, description, image, free/paid flag).
- Lesson creation and reordering.
- Content blocks: text, images, video (first-party hosted via bunny.net, with signed URLs), file attachments, multiple-choice quizzes.
- Learner progress (lesson view/completion, quiz scores).
- Free and paid enrollment via Razorpay (single payment provider for MVP), with a platform commission computed on every sale.
- Certificate generation: server-side-generated PDF, downloadable, with a public verification URL.
- Email notifications (new course assigned, new lesson published, certificate earned) sent via Resend.
- Basic admin dashboard (user list, course list, enrollment/revenue overview) scoped per organization, plus a read-only platform-owner view across organizations.

## Explicitly deferred (no code, no placeholder)

- AI-powered tools (auto-grading, content suggestions, chat tutors).
- Live classes and real-time video conferencing.
- Mobile apps (iOS, Android).
- Code execution environments.
- SCORM compliance and xAPI tracking.
- Advanced analytics and learning science metrics.
- Community features (forums, peer discussion).
- White-labeling / custom domains per organization.
- SSO, LDAP, or third-party authentication (Google OAuth and MFA are deferred past MVP; email/password only for now).
- Real-time collaboration (co-authoring lessons).
- Advanced grading rubrics and manual grading workflows.
- The full admin console (feature flags, quota management, CLI, backups, observability) — the MVP's admin dashboard is a minimal read-only view only.
- Organization-level platform subscription billing — monetization is commission-based only for MVP.
