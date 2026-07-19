---
task: 7
name: human-doc
parallel_group: 6
depends_on: [1, 2, 3, 4, 5, 6]
issue: TBD
---

# Task 7: Human-readable plan overview

## What to build

A single human-to-human explainer document for the `lms-mvp` plan, aimed at a developer (or the author) coming back after weeks with no context — someone who needs to *reason about* the system, not reproduce it from a spec. This is not an agent-executable task in the usual sense: there's no code to write. The deliverable is one Markdown document.

Write it to `docs/plans/lms-mvp.md` (create `docs/plans/` if it doesn't exist).

The document should:

- Open with two or three plain-language sentences: what this product is (an independent online learning platform MVP — not a clone of any existing named product), who it's for (organizations/teachers selling and running courses, and the learners taking them), and why this plan exists (it's the MVP slice — Tasks 1-6 — of a larger 12-task roadmap in `plan.md`, scoped down to what's needed for a first real teacher-sells-a-course-learner-completes-it loop).
- Explain how the system fits together at a reasoning level, not an implementation level: the Go backend that serves both server-rendered HTML and a JSON API; Supabase as the Postgres + Auth provider; Postgres Row-Level Security as the wall between organizations' data; bunny.net for video vs. Supabase Storage for everything else; Redis-backed background jobs for email and webhook processing; Razorpay for payments with entitlement-gated access. Explain *why* these fit together (e.g. why RLS matters, why webhooks — not the browser return URL — grant access), not just list them.
- Include a Mermaid diagram (```mermaid fence, so it renders on GitHub) showing the six-task dependency flow: Task 1 → Task 2 → Task 3 → Task 4 → (Task 5 and Task 6 in parallel). A second small diagram is optional if it clarifies the request-time RLS flow (JWT → Go middleware → session variables → RLS-scoped query) — include it only if it earns its space.
- Include a short "Decisions in plain words" section covering the two or three choices that most shape the MVP, restated for a human reader (not copy-pasted from `main-plan.md`'s decision log): e.g. why RLS instead of app-only checks, why entitlements are only granted from verified webhooks, why the block editor is deliberately limited to five block types for now.
- End with pointers, not duplicated content: link `plans/lms-mvp/main-plan.md` (the spec and full decision log) and the six task files (`task-1-product-identity.md` through `task-6-commerce.md`). Do not restate their acceptance criteria here — if this doc and the spec ever disagree, the spec wins; this doc only explains.
- Keep it proportional: this is a six-task MVP plan, not a huge system — aim for roughly half a page to a page of prose plus the one diagram, not an exhaustive section-per-task breakdown.

## Acceptance criteria

- [ ] `docs/plans/lms-mvp.md` exists and opens with a plain-language summary a non-implementer can follow.
- [ ] The doc explains how the major pieces (Go/Gin, Supabase Postgres+Auth, RLS, bunny.net/Supabase Storage split, Redis jobs, Razorpay) fit together and why, without listing them as a dry stack table.
- [ ] A Mermaid diagram renders the Task 1→2→3→4→(5‖6) dependency flow.
- [ ] A "Decisions in plain words" section covers the RLS-for-isolation, webhook-only-entitlement, and reduced-block-editor decisions (or others judged most load-bearing) in one sentence each with their why.
- [ ] The doc links to `main-plan.md` and the six task files rather than duplicating their content, and contains no frontmatter, acceptance-criteria checklists, or spec jargon of its own.
- [ ] Length is proportional to the plan's size (roughly half a page to a page plus diagram(s)) — not a restatement of every task file.

## Commit convention

Your commit message MUST include `Closes #<issue-number>` (issue number to be filled in when published to GitHub) when the task's GitHub issue closes.
