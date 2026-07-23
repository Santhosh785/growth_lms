# Backup & Restore Policy

This document defines the database and media backup policy for the platform
(Task 10). It builds on the operational CLI (`internal/cli`) already shipped —
`app backup` and `app restore` wrap `pg_dump`/`pg_restore` — and specifies
*when* and *how often* those run, how long copies are kept, and how a restore is
rehearsed.

## What is backed up

| Asset | Source of truth | Mechanism | Owner |
| --- | --- | --- | --- |
| PostgreSQL (all tenant data, auth, audit log) | Supabase Postgres | `app backup` (`pg_dump`, custom format `-Fc`) | Platform ops |
| Uploaded media (video, images, files) | Bunny Stream / object storage | Provider-side replication + weekly manifest export | Platform ops |
| Secrets / config | `.env`, secret manager | Out of band; see Secret management (Task 11) | Platform ops |

The audit log (`audit_events`) is inside the Postgres dump, so administrative
actions — user suspensions, org deactivations, course takedowns, plan changes —
are captured in every database backup with no extra step.

## Schedule & retention

Database:

- **Hourly** incremental (Supabase point-in-time recovery / WAL) — retained **7 days**.
- **Daily** full logical dump via `app backup` at 02:00 UTC — retained **30 days**.
- **Weekly** full dump copied to a second region/bucket — retained **90 days**.
- **Monthly** full dump — retained **12 months** for compliance.

Media:

- Rely on the storage/CDN provider's built-in redundancy for durability.
- **Weekly** export of the object manifest (keys + checksums) so a lost object
  can be detected and, where the source still exists, re-uploaded.

Retention is enforced by the backup job (a cron/`app`-driven task); older
artifacts past their window are pruned automatically. Any job that cannot
complete raises a `storage`/`database` category alert (see `system_alerts` and
the admin alerts surface) so a silent backup gap is impossible.

## Running a backup manually

```
app backup                      # writes ./backups/<timestamp>.dump
app backup /secure/path.dump    # explicit output path
```

The command reads `LMS_DATABASE_URL`. Use the **direct** (non-pooler)
connection string for `pg_dump`, not the transaction-mode pooler.

## Restore & disaster recovery

```
app restore /secure/path.dump   # pg_restore into LMS_DATABASE_URL
```

Restore procedure:

1. Provision a fresh, empty Postgres (never restore over a live database).
2. `app restore <dump>`.
3. `app migrate version` — confirm the schema version matches the app build.
4. `app health` — confirm Postgres + Redis connectivity.
5. Point a staging instance at the restored DB and smoke-test login, catalog,
   player, and one checkout before promoting.

**Recovery objectives:** RPO ≤ 1 hour (WAL/PITR), RTO ≤ 4 hours (full logical
restore + verification).

## Restore rehearsal

A restore that has never been tested is not a backup. **Monthly**, restore the
latest daily dump into a throwaway environment, run the smoke test above, and
record the wall-clock restore time. If restore time approaches the RTO,
escalate (parallel restore, smaller/partitioned dumps, or PITR-first recovery).

## Responsibilities

- Backups run under the platform-ops role, not any tenant.
- Restores touching tenant data are a platform-owner action and must be recorded
  (change ticket + audit note).
- Never commit dumps to the repository or attach them to issues — they contain
  every tenant's data.
