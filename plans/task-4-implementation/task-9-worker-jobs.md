---
task: 9
name: worker-jobs
parallel_group: 3
depends_on: [5, 6]
issue: N/A (disk-only draft, no GitHub remote configured)
---

# Task 9: Scheduled publish sweep + Bunny webhook processing

## What to build

Extend `internal/worker/worker.go` (register new task types on the
existing `asynq.NewServeMux()`; do not change its overall structure).

`internal/worker/publish.go`:
- A periodic enqueue loop (a `time.Ticker`-driven goroutine started
  alongside `srv.Run(mux)` in `Run()`, or `asynq`'s built-in periodic task
  manager if it fits more naturally — pick whichever is less new
  machinery) that enqueues a `TypeSweepScheduledPublish` task once a
  minute.
- The task handler queries (via a pool connection, RLS bypassed at the
  admin/worker level — same trust boundary as any other backend
  background job) `courses WHERE status='scheduled' AND publish_date <=
  now()`, and for each match, in one transaction per course: takes a
  `course_versions` snapshot (reuse `models.CourseVersionRepo.Snapshot`
  from Task 5), sets `status='published'`, `published_at=now()`. A course
  reverted to `review` before the sweep runs simply stops matching the
  WHERE clause — no cancellation bookkeeping needed.
- Must also enforce the "publish blocks on incomplete video processing"
  rule from the spec: skip (log, don't fail the whole sweep) any course
  still containing a non-`ready` video block asset, leaving it
  `scheduled` for a later sweep pass, rather than publishing a broken
  course.

`internal/worker/bunny_webhook.go`:
- A `TypeBunnyTranscodeComplete` task handler (enqueued by the HTTP
  webhook endpoint in Task 8/10 AFTER signature verification — this
  worker code must never be reachable from an unverified caller, matching
  the "verified provider webhook only" rule the spec explicitly compares
  to the payments rule) that updates the matching `assets` row:
  `processing_status='ready'`, fills `duration`/`thumbnail_url` from the
  webhook payload, or `processing_status='failed'` on a transcode-failure
  payload.

## Acceptance criteria

- [ ] The sweep task runs on a fixed interval (~1 minute) without manual
      triggering, and publishing a due course sets both `published_at`
      and creates exactly one new `course_versions` row per publish.
- [ ] A course whose status flips back to `review` before its
      `publish_date` sweep never gets published.
- [ ] A `scheduled` course containing a non-ready video block is skipped
      by the sweep (stays `scheduled`) rather than publishing with a
      broken video.
- [ ] The Bunny webhook task handler is only ever invoked with
      already-signature-verified payloads (verification happens in the
      HTTP handler layer, not here) — this task's code has no HTTP-level
      trust decision to make, only the DB update.
- [ ] Worker package still builds/runs standalone exactly as before (no
      change to `cmd/worker`'s entrypoint contract).

## Commit convention

This is a disk-only plan (no GitHub remote configured) — commit normally
with a descriptive message; no `Closes #` trailer applies.
