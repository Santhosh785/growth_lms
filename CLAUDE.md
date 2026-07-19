# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository state

This repository currently contains only `plan.md` — there is no source code, build tooling, or test suite yet. Do not assume any commands, frameworks, or file layout exist beyond what is listed below; verify before referencing anything as if it already exists.

## What `plan.md` is

`plan.md` is the product and engineering roadmap for building an independent, production-ready online learning platform (comparable in scope to LearnHouse, but under its own name/branding/infrastructure — no LearnHouse code, names, or assets are to be reused). Read it in full before starting implementation work; it defines the phased task breakdown, acceptance criteria, and MVP boundary that any new code should follow.

Key points from the plan:

- **Intended stack**: Go backend (net/http / Gin / Fiber), HTML + HTMX + Tailwind CSS frontend, Supabase for PostgreSQL/Auth/Storage, Razorpay/Stripe for payments.
- **Roles**: platform owner, organization owner, teacher/creator, learner, moderator.
- **Execution order**: Tasks 1-12, grouped into Phase A (Foundation: repo/infra/auth/tenancy), Phase B (MVP: courses/learner journey/commerce), Phase C (engagement/growth), Phase D (advanced/admin/ops), Phase E (hardening/launch).
- **Recommended MVP boundary**: only Tasks 1-6 — single organization, email auth, teacher/learner roles, course+lesson authoring, learner progress, free/paid enrollment with one payment provider, basic certificates and email, basic admin page. Do not build AI features, live classes, mobile apps, code execution, SCORM, or advanced analytics before the MVP ships.
- Organization data isolation and server-side permission enforcement are called out repeatedly as non-negotiable from Task 3 onward — every organization-owned table must enforce tenant isolation, and payment/enrollment access must only ever be granted after verified provider webhook events, never from browser return URLs.

## Working in this repo

Once implementation begins, update this file with real build/lint/test commands and the actual architecture (package layout, service boundaries, migration workflow) as they're established — do not carry these placeholder notes forward once they're superseded by real code.
