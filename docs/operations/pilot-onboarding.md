# Pilot Onboarding

Checklist for bringing the first pilot organizations onto the platform (plan.md
Task 12). Keep the pilot small (1–3 orgs) so support is high-touch and feedback
is fast.

## Before onboarding

- [ ] Staging validated and production launch-checklist green
      ([deployment.md](deployment.md)).
- [ ] Monitoring + alerting live and paging the on-call
      ([monitoring.md](monitoring.md)).
- [ ] Backups running, one restore rehearsed ([backup-policy.md](backup-policy.md)).
- [ ] `/privacy` and `/terms` published; support contact set.
- [ ] Payment provider in the correct mode for the pilot (live keys only if the
      pilot transacts real money).

## Per-organization setup

1. **Create the org** and assign an organization owner. Verify tenant isolation
   (the owner sees only their org's data).
2. **Branding** — set org name, logo, theme, and custom domain if used (Task 8).
3. **Plan & limits** — assign the plan and confirm quota/limits fit the pilot's
   size (Task 10 admin).
4. **Seats** — invite the org's teachers/creators and a few learners; confirm
   invitation emails arrive (Resend).
5. **Smoke test the core journey** *as the org*:
   - Create a course → add chapters/lessons/blocks → upload a video (confirm it
     transcodes to `ready`) → publish.
   - Create a free and a paid offer; run a **test** checkout and confirm access
     is granted only after the webhook.
   - A learner enrolls, completes a lesson, passes a quiz, earns a certificate;
     verify the certificate at `/certificates/verify`.
6. **Feature flags** — enable only the modules the pilot needs (AI, code exec,
   SCORM, simulations, podcasts are flag-gated).

## During the pilot

- Weekly check-in with each org owner; log issues as GitHub issues.
- Watch dashboards for the pilot's traffic; compare latency to `loadtest/`
  baselines.
- Track: activation (first course published), learner completion, checkout
  success rate, support ticket volume.

## Exit criteria (pilot → general availability)

- [ ] No open SEV1/SEV2 issues.
- [ ] Checkout success rate and webhook reliability at target.
- [ ] Backups + restore proven under real data.
- [ ] Support load sustainable; docs cover the common questions.
- [ ] Pilot orgs would recommend / continue.
