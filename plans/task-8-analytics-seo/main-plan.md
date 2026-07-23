# Task 8: Analytics, search, SEO, themes, and public pages

## Goal

Per plan.md Task 8 (lines 225-241): event tracking + creator/org/platform
analytics, background aggregation, cross-entity search, SEO surfaces
(sitemap/robots/OG/JSON-LD), a configurable public landing page per org,
theme tokens/branding, custom domains with verification, and an embeddable
course catalog/checkout link.

## Scope decision

User confirmed: Task 8 only (not 9/10). Task 9's AI/sandbox/SCORM
infrastructure choices are out of scope here.

## Migration (000009_analytics_seo_growth)

- `analytics_events`: org_id, event_type, actor_user_id (nullable),
  course_id (nullable), lesson_id (nullable), metadata jsonb, created_at.
  RLS: insert by any org member (any authenticated action can emit),
  select restricted to org owner/teacher/moderator (creator analytics).
- `analytics_daily_rollups`: org_id, day, course_id (nullable = org-wide),
  metric text, value bigint, unique(org_id, day, course_id, metric). RLS
  select same as analytics_events; only the worker (admin pool) writes.
- `organizations` new columns: logo_url, favicon_url, theme_json,
  custom_domain, domain_verification_token, domain_verified_at,
  meta_description, og_image_url.
- `org_pages`: org_id, slug, title, content_html, is_published,
  created_at, updated_at. unique(org_id, slug). RLS: public SELECT when
  is_published, owner/teacher manage.
- tsvector columns + GIN indexes on `courses.title/description`,
  `lessons.title`, `discussion_threads.title` (existing tables) for search.

## Phases

1. Migration + down migration, apply and verify.
2. Model repos: AnalyticsEventRepo, AnalyticsRollupRepo, OrgPageRepo,
   SearchRepo, OrgRepo branding/domain methods.
3. Event tracking helper (fire-and-forget insert from existing handlers:
   course view, enrollment, lesson start/completion, search, purchase,
   refund, certificate issue) + creator/org analytics dashboard handlers +
   worker daily-rollup sweep (mirrors abandon-orders sweep pattern).
4. Search: GET /api/orgs/:org_slug/search (courses/lessons/discussions)
   server-rendered results page.
5. SEO: GET /sitemap.xml, GET /robots.txt, OG meta + JSON-LD in course/org
   public templates; embeddable catalog GET /embed/orgs/:org_slug/catalog
   (iframe-safe, no nav chrome) + checkout link generator.
6. Branding/theme settings page (owner-only) + landing-page builder
   (org_pages CRUD) + public org page renderer using theme_json.
7. Custom domain: settings UI to set domain, generates verification
   token; POST verify endpoint does a real `net.LookupTXT` check against
   `_growthlms-verify.<domain>`.
8. Tests: RLS isolation for analytics_events/org_pages, rollup worker
   unit test, search query test, sitemap/robots content test.

## Non-goals (deferred to Task 9/10)

AI-assisted content, SCORM, code execution, admin console, CLI.

## Status: done

All 8 phases implemented on `task-7-communities` branch (continuing the
existing branch rather than cutting a new one, since Task 7 hadn't been
merged to main yet):

- Migration `000009_analytics_seo_growth` (up/down), applied and verified.
- Models: `AnalyticsEventRepo`, `AnalyticsRollupRepo`, `OrgPageRepo`,
  `SearchRepo`, `OrgRepo` branding/domain methods, `CourseRepo.ListPublished`.
- Event tracking wired into: `EnrollCourse`, `CompleteLesson`,
  `CourseLearnPage`, `LessonPlayerPage`, certificate issuance,
  Razorpay payment.captured (purchase) and refund.processed (refund),
  and the search handler itself.
- Worker: hourly `analytics_daily_rollups` sweep (`internal/worker/analytics.go`).
- HTTP: `GET /api/orgs/:org_slug/analytics`, `/search`, `/branding`
  (GET/PATCH), `/domain` + `/domain/verify`, `/pages` CRUD,
  `/courses/:courseId/offers/:offerId/embed-link`; public
  `/o/:org_slug`, `/o/:org_slug/pages/:slug`, `/o/:org_slug/sitemap.xml`,
  `/o/:org_slug/robots.txt`, `/embed/o/:org_slug/catalog`.
- Tests: RLS isolation for analytics_events/org_pages/domain resolution
  (`internal/models/rls_isolation_analytics_test.go`, requires
  `LMS_TEST_DATABASE_URL` to actually run — same precedent as the rest of
  the suite) + a pure-function unit test for HTML escaping.
- `go build ./...`, `go vet ./...`, `go test ./...` all pass.

Not built: a dedicated server-rendered settings UI for branding/domain/
landing-page-builder (JSON APIs only) — deferred as a UI polish pass,
consistent with several Task 6 admin capabilities also being JSON-API-only.
