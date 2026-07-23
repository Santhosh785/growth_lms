# Incident Response & Support

Procedures for handling production incidents and user support (plan.md Task 12).

## Severities

| Sev | Definition | Examples | Response |
| --- | --- | --- | --- |
| **SEV1** | Platform down or data/payment integrity at risk | site down, checkout broken, tenant-isolation breach, data loss | Page on-call immediately; all-hands |
| **SEV2** | Major feature broken, no data risk | video playback down, email not sending, one org degraded | Page on-call; fix same day |
| **SEV3** | Minor/degraded, workaround exists | slow page, cosmetic bug | Ticket; next business day |

## On-call flow

1. **Detect** — alert fires ([monitoring.md](monitoring.md)) or a report comes in.
2. **Triage** — assign a severity; open an incident channel/thread; name an
   incident lead.
3. **Mitigate first** — restore service before root-causing. Levers:
   - Roll back to the previous image tag ([deployment.md](deployment.md)).
   - Check `/readyz`, `./bin/app health`, `./bin/app status`.
   - Scale/restart the affected service (`docker compose ... up -d <svc>`).
4. **Communicate** — post status updates on cadence; notify affected orgs for
   SEV1/2.
5. **Resolve & verify** — confirm via the launch-checklist items for the
   affected surface.
6. **Post-incident review** — within 48h for SEV1/2: timeline, root cause,
   action items. Blameless.

## Common incidents → runbook

| Symptom | First checks | Runbook |
| --- | --- | --- |
| Payments not granting access | `webhooks` alerts; webhook signature/secret; provider dashboard | [secret-management.md](secret-management.md) (webhook rotation) |
| DB errors / `readyz` red | `./bin/app health`; Supabase status; pool alerts | [deployment.md](deployment.md) |
| Suspected secret leak | rotate immediately | [secret-management.md](secret-management.md) |
| Bad deploy | roll back image tag | [deployment.md](deployment.md) |
| Data corruption / loss | stop writes; restore | [backup-policy.md](backup-policy.md) |
| Brute-force / abuse | `auth` alerts; rate limits; suspend via admin | Task 10 admin actions |

## Support intake

- **Users** report via the support contact (see [SECURITY.md](../../SECURITY.md)
  for the current placeholder; a dedicated support address is published at
  launch). Security issues follow SECURITY.md's disclosure process.
- **Triage** support requests to SEV3 by default; escalate if they reveal a
  SEV1/2. Abuse/content reports use the Task 7 moderation queue; account
  actions (suspend/deactivate/takedown) use the Task 10 admin surface and are
  captured in the audit log.
