# Upgrade & Migration Process

How to roll a new build of the platform to a running environment safely
(Task 10). The schema is versioned with `golang-migrate` (numbered files under
`db/migrations/`), driven by the `app migrate` CLI.

## Principles

- **Migrations are forward-only in production.** `down` migrations exist and are
  tested, but a production rollback of *data* schema is a recovery action
  (restore from backup), not a routine `migrate down`.
- **Expand / contract.** A change that would break the currently-running code is
  split across two releases: first add the new column/table and dual-write
  (expand), ship the code that uses it, then remove the old column in a later
  release (contract). No single deploy both adds a NOT NULL column and depends
  on it being populated.
- **Every migration has a reviewed `.up.sql` and `.down.sql`**, and is applied
  by the CLI, never by hand-editing the database.

## Migration commands

```
app migrate up            # apply all pending migrations
app migrate down 1        # revert the most recent migration (dev/staging)
app migrate version       # print current schema version + dirty flag
app migrate force <v>     # clear a dirty state after a failed migration (careful)
```

`app setup` runs `migrate up` as part of first-time provisioning.

## Standard upgrade procedure

1. **Announce / window.** For migrations that lock or rewrite large tables,
   schedule a low-traffic window. Additive migrations (new table, new nullable
   column, new index built `CONCURRENTLY`) generally need no window.
2. **Back up first.** `app backup` (see [backup-policy.md](backup-policy.md)).
   Confirm the dump exists and is non-empty before proceeding.
3. **Migrate staging.** Apply `app migrate up` on staging, run the smoke test
   (login, catalog, player, checkout, webhook), and check `app migrate version`
   is clean (not dirty).
4. **Migrate production.** `app migrate up`. Migrations run inside
   transactions where the DDL allows; a failure leaves the version marked dirty
   — do **not** deploy new app code until it is resolved.
5. **Deploy code.** Roll out the new binary. With the expand/contract rule, the
   new schema is compatible with both the old and new code, so ordering between
   step 4 and 5 is forgiving.
6. **Verify.** `app health`, `app status`, watch `/metrics`
   (`lms_http_requests_total{status="5xx"}`, `lms_panics_total`) and the admin
   alerts surface for a spike. Check the audit log for expected activity.
7. **Roll back if needed.** Redeploy the previous binary (fast, safe — code is
   stateless). Only revert the schema if the migration itself is the fault, and
   prefer a restore from the step-2 backup over `migrate down` on live data.

## Zero-downtime notes

- Build indexes with `CREATE INDEX CONCURRENTLY` in a standalone migration (it
  cannot run inside a transaction block — keep it in its own file).
- Add columns as nullable or with a cheap default; backfill in a separate,
  batched migration or a one-off job, not in the DDL statement.
- Rename via add-new + dual-write + backfill + drop-old across releases, never a
  bare `RENAME` that the old code can't see.

## Observability during upgrades

The build exposes request-ID-correlated structured logs, a `/metrics` endpoint
(request rate/latency histogram, `lms_panics_total`), and operational alerts.
Watch all three through an upgrade; a rising 5xx class or panic counter is the
signal to roll the code back.
