# Product Brief

## Product model

Growth LMS is a hosted, multi-tenant SaaS platform operated by the platform owner. Multiple customer organizations sign up and run their own school/academy workspace on shared infrastructure. This is not self-hosted software distributed to customers — customers never receive a copy of the code; they use the hosted product.

## Target customer

Educational teams, academies, and businesses that want to sell and run courses without operating their own infrastructure.

## Problem solved

Educators need a learning platform they can start using immediately — self-service signup, no infrastructure to manage, no complex enterprise pricing, and no vendor lock-in on their course content.

## First-release scope (MVP boundary)

- Public multi-tenant SaaS from launch: any customer can sign up and create their own organization, with full data isolation between organizations enforced at the database level (Postgres Row-Level Security), not just application code.
- Email/password authentication (OAuth and MFA deferred past MVP).
- Full role model: platform owner, organization owner, teacher/creator, moderator, learner.
- Course and lesson authoring with text, image, video (first-party hosted), file, and quiz content blocks.
- Learner progress tracking, including quiz scores and lesson completion.
- Free and paid course enrollment via Razorpay.
- Certificate generation: server-side-rendered PDF, downloadable, with a public verification URL.
- Basic email notifications (course assigned, lesson published, certificate earned).
- A basic, read-only admin dashboard: organization-scoped (users, courses, enrollment/revenue overview) and a platform-owner cross-organization view.

Everything beyond this — AI tools, live classes, mobile apps, code execution, SCORM, advanced analytics, communities, white-labeling/custom domains, SSO, real-time collaboration, the full admin console (feature flags, quotas, CLI, backups) — is explicitly deferred past this MVP.

## Pricing model direction

The platform monetizes via a commission on course sales: a percentage/fixed fee taken from each learner payment, computed and recorded server-side alongside every order. Organizations are not charged a separate subscription fee to use the platform in the MVP. The exact commission rate is a business decision to be set as configuration, not hardcoded.

## Success metric for MVP

A new organization can sign up unassisted, a teacher in that organization can create a course, invite learners, deliver content, track progress, and issue certificates — with zero cross-tenant data visibility between organizations.
