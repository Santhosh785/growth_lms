---
task: 4
name: course-domain
parallel_group: 4
depends_on: [3]
issue: TBD
---

# Task 4: Course Domain, Media, Authoring, and Publishing

## What to build

**Scope**: Implement the complete course authoring and content management system for teachers, including schema design with org-scoped Row-Level Security, course lifecycle management (draft/review/scheduled/published/unpublished), a block-based content editor supporting exactly 5 block types, signed-URL-based media uploads to bunny.net (video) and Supabase Storage (images/files), course duplication, version history, and collections/bundles grouping. Learner-facing playback, quiz-taking, progress tracking, and access control decisions are deferred to Tasks 5 and 6.

**Prerequisite**: Task 3 (auth, organizations, tenancy, and permission-enforcement middleware with RLS session-variable pattern) must be complete before starting.

### Database Schema & Row-Level Security

- Create `courses` table with columns: `id` (UUID), `org_id` (UUID, foreign key), `title`, `description`, `cover_image_url` (signed/private), `category_id`, `status` (enum: draft/review/scheduled/published/unpublished), `created_by` (user_id), `updated_at`, `published_at`, `archived_at`, soft-delete support. Add Postgres RLS policy: org members can view/edit courses in their org; outsiders cannot access.
- Create `chapters` table: `id`, `course_id`, `org_id`, `title`, `sort_order` (numeric, supports fractional ordering for drag-reorder), `created_by`, `updated_at`. RLS: org-scoped access like courses.
- Create `lessons` table: `id`, `chapter_id`, `course_id`, `org_id`, `title`, `sort_order`, `created_by`, `updated_at`. RLS: org-scoped.
- Create `blocks` table: `id`, `lesson_id`, `course_id`, `org_id`, `type` (enum: text|image|video|file|quiz), `content` (JSONB), `sort_order`, `created_by`, `updated_at`. JSONB structure varies by type:
  - `text`: `{ "html": "..." }` (allow safe HTML)
  - `image`: `{ "asset_id": "...", "alt_text": "..." }` (references `assets` table)
  - `video`: `{ "asset_id": "...", "duration": 120, "thumbnail_url": "..." }` (bunny.net video)
  - `file`: `{ "asset_id": "...", "filename": "..." }` (Supabase Storage file)
  - `quiz`: `{ "questions": [{ "id": "...", "type": "mcq|short_answer|...", "question": "...", "answers": [...], "correct_answer_index": 0 }] }` (authoring only; scoring deferred to Task 5)
- Create `assets` table: `id`, `org_id`, `course_id`, `type` (enum: image|video|file), `filename`, `size_bytes`, `mime_type`, `storage_provider` (bunny|supabase), `storage_key` (path in bunny.net or S3-style path in Supabase), `signed_url` (cached), `signed_url_expires_at`, `created_by`, `updated_at`. RLS: org members can list/view assets in their org; non-org members cannot.
- Create `categories` table: `id`, `org_id`, `name`, `slug`. RLS: org-scoped.
- Create `tags` table: `id`, `org_id`, `name`, `slug`. RLS: org-scoped.
- Create `course_tags` junction table: `course_id`, `tag_id`, `org_id`. RLS: org-scoped.
- Create `course_versions` table: `id`, `course_id`, `org_id`, `version_number`, `snapshot` (JSONB storing full course state: chapters, lessons, blocks at snapshot time), `created_by`, `created_at`. RLS: org-scoped. Snapshots on each publish; allow restore-to-previous-version (creates new version, does not overwrite).
- Create `course_prerequisites` table: `course_id`, `prerequisite_course_id`, `org_id`. RLS: org-scoped.
- Create `course_completion_rules` table: `id`, `course_id`, `org_id`, `rule_type` (enum: all_lessons|percent_lessons|all_quizzes|percent_quizzes), `threshold` (int, e.g., 80 for 80%), `created_by`, `updated_at`. RLS: org-scoped.
- Create `collections` table: `id`, `org_id`, `name`, `description`, `created_by`, `updated_at`. RLS: org-scoped.
- Create `collection_courses` junction table: `collection_id`, `course_id`, `org_id`, `sort_order`. RLS: org-scoped.

All tables MUST have an `org_id` column and Postgres RLS policies enforcing `org_id = current_setting('app.org_id')::uuid` (using the session variable set by Task 3's middleware). Never rely on Go-side authorization; RLS is the isolation boundary.

### Course Lifecycle & CRUD Operations

- Implement course creation endpoint (POST /api/courses): accept title, description, category, tags; create in `draft` status; return course object.
- Implement course metadata update (PATCH /api/courses/:id): update title, description, category, tags, cover_image.
- Implement course status transitions:
  - `draft` → `review`: teacher submits for review (optional moderation step).
  - `review` → `published` or back to `draft`.
  - `published` → `scheduled` (set publish_date in future): course visible only after publish_date.
  - `published` → `unpublished`: revoke learner visibility.
  - `draft`/`review`/`scheduled`/`unpublished` → `archived`: soft-delete, remove from learner view but preserve in author's archive list.
- Implement course delete (hard delete only if status is `draft` and no attempts exist; otherwise reject or require archival first).
- Implement course archive/unarchive toggle.

### Chapter and Lesson Management

- Chapter create (POST /api/courses/:courseId/chapters): accept title; auto-assign `sort_order` as next fractional value (e.g., 1.0, 2.0, 3.0 or 1.5 between 1.0 and 2.0 for drag-reorder).
- Chapter update (PATCH /api/courses/:courseId/chapters/:id): update title, sort_order.
- Chapter delete: soft-delete or reject if lessons exist (decide per architecture; recommend preventing cascade delete to avoid data loss).
- Lesson create (POST /api/courses/:courseId/chapters/:chapterId/lessons): accept title; auto-assign sort_order within chapter.
- Lesson update (PATCH /api/courses/:courseId/chapters/:chapterId/lessons/:id): update title, sort_order.
- Lesson delete: similar policy to chapters.
- Reorder endpoint (POST /api/courses/:courseId/chapters/reorder or POST /api/courses/:courseId/chapters/:chapterId/lessons/reorder): accept array of IDs with new sort_order values; update in transaction.

### Block-Based Content Editor (Exactly 5 Block Types)

- Block create (POST /api/courses/:courseId/chapters/:chapterId/lessons/:lessonId/blocks): accept type (text|image|video|file|quiz) and initial content; return block object.
- Block update (PATCH /api/courses/:courseId/.../blocks/:id): update content (JSONB structure per type), sort_order.
- Block delete (DELETE /api/courses/:courseId/.../blocks/:id).
- Block reorder (POST /api/courses/:courseId/.../lessons/:lessonId/blocks/reorder): update sort_order for multiple blocks.
- Text block: simple HTML field; sanitize input (e.g., allow `<p>`, `<strong>`, `<em>`, `<ul>`, `<li>`, but reject `<script>`, event handlers).
- Image block: upload flow (see Media Upload section); store asset_id + alt_text.
- Video block: upload to bunny.net via signed URL; store asset_id, duration (metadata from bunny API or transcoding webhook), thumbnail.
- File block: upload to Supabase Storage via signed URL; store asset_id + filename.
- Quiz block: store array of questions with type, question text, answer options, and correct-answer index. No scoring logic here (Task 5's job); this task owns authoring only.

### Autosave & Explicit Publish

- Autosave endpoint (POST /api/courses/:courseId/.../blocks/:id/autosave): save block content without changing course status; update `updated_at` only, not `published_at`.
- Publish endpoint (POST /api/courses/:courseId/publish): atomically transition course from `draft`/`review`/`unpublished` to `published`, set `published_at`, and create a snapshot in `course_versions` table. Publish is distinct from autosave; drafts are never visible to learners.
- Unpublish endpoint (POST /api/courses/:courseId/unpublish): transition `published` back to `unpublished`; learners lose access immediately.

### Course Preview Mode

- Implement teacher-only preview endpoint (GET /api/courses/:courseId/preview): render course structure as learners would see it (all chapters, lessons, blocks in published order) but only accessible to org members with teacher/owner role. Preview does not require `published_at` date to be in the past; preview is always available for draft/review/scheduled courses for authoring teams.
- HTMX UI: preview button on course editor that opens a read-only view (styled like learner view) in a modal or new tab.

### Media Upload Flow (Signed URLs)

- **Video uploads to bunny.net**:
  - Endpoint (POST /api/media/upload/video): returns signed upload URL (time-limited, 30 min expiry) for direct browser upload to bunny.net. Include bunny collection ID (per org if multi-tenant) and API key in server-side signing, never expose to browser.
  - On successful upload to bunny.net, webhook or polling notifies backend; backend creates `assets` record pointing to bunny.net storage key.
  - Video metadata (duration, thumbnail) fetched from bunny API or embedded in webhook.
  - Access: video signed URL is generated on-demand (PATCH /api/assets/:id/refresh-url) and cached for 1 hour. Private unpublished video URLs must be short-lived (< 5 min for direct playback access).

- **Other file uploads (images, files) to Supabase Storage**:
  - Endpoint (POST /api/media/upload): returns signed Supabase Storage URL (time-limited, 30 min expiry) for direct browser upload. Include bucket, path, and restricted permissions (object-specific).
  - Uploaded files stored in path like `org/{org_id}/courses/{course_id}/{asset_id}/{filename}`.
  - Backend creates `assets` record after receiving confirmation.
  - Access: generate signed URL on-demand (PATCH /api/assets/:id/refresh-url) with appropriate expiry (1 hour for preview/draft, short-lived < 5 min for immediate access).

- Endpoint (POST /api/assets/:assetId/refresh-url): regenerate signed URL; used when cached URL expires.
- Assets must not be publicly readable; all access via signed URLs. Supabase Storage bucket must have public access disabled; RLS on `assets` table prevents non-org members from requesting signed URLs for other orgs' assets.

### Private Media Access Control

- Unpublished/draft course content media must not be fetchable by non-authors, even via direct storage URL.
- Signed URLs for draft-course assets must be scoped to the asset and time-limited (< 5 minutes for preview, 1 hour for in-editor work).
- Published course assets can have longer-lived signed URLs (1 hour) if the course is `published`, but revoke immediately upon `unpublish`.
- Implement Supabase RLS policy on `assets`: users can only access assets in their org.
- Bunny.net: rely on signed URL expiry + org-level path scoping (bunny collection per org if available, else rely on URL signing).

### Course Duplication

- Endpoint (POST /api/courses/:courseId/duplicate): create new course as deep copy of source (chapters, lessons, blocks, assets all duplicated). New course starts in `draft` status. Assets are NOT duplicated in storage; instead, new course's blocks reference the same asset IDs (or if full isolation required, re-upload, but recommend shared asset references for MVP to avoid storage bloat). Return new course ID.

### Version History

- Endpoint (GET /api/courses/:courseId/versions): list all versions for course, ordered by most recent first. Include version_number, created_by, created_at, and a preview of snapshot changes (e.g., "10 blocks modified").
- Endpoint (GET /api/courses/:courseId/versions/:versionId): return full snapshot JSONB.
- Endpoint (POST /api/courses/:courseId/versions/:versionId/restore): restore course to prior version (creates new version with restored content, does not revert; allows undo).
- Snapshot structure: full course state as nested JSON (chapters with lessons with blocks), allowing diffs and restoration without re-querying database.

### Collections/Bundles

- Collections are org-scoped groupings of courses (no pricing, no access-control logic; just organizational grouping).
- Endpoint (POST /api/collections): create collection with name, description.
- Endpoint (PATCH /api/collections/:id): update collection metadata.
- Endpoint (DELETE /api/collections/:id): delete collection (remove junction records).
- Endpoint (POST /api/collections/:id/courses): add course to collection (create `collection_courses` record with sort_order).
- Endpoint (DELETE /api/collections/:id/courses/:courseId): remove course from collection.
- Endpoint (GET /api/collections/:id/courses): list courses in collection with sort_order.
- Endpoint (POST /api/collections/:id/courses/reorder): reorder courses within collection.
- Note: Storefront presentation of collections (learner-facing "browse collections") is Task 6/8's job; this task only stores the structure.

### HTMX Server-Rendered UI (Per Task 2 Architecture)

- Course editor page (GET /courses/:id/edit): render HTMX-driven editor with:
  - Course metadata form (title, description, category, tags, cover image upload).
  - Chapter list with add/reorder/edit/delete; expandable to show lessons.
  - Lesson list within chapter; add/reorder/edit/delete.
  - Block editor: render blocks by type; inline edit for text, upload UI for images/videos/files, quiz question builder for quiz blocks.
  - Autosave on blur/change (HTMX hx-trigger="change delay:1s" or similar); show "saving..." indicator.
  - Draft/review/publish/unpublish buttons; publish shows confirmation dialog.
  - Preview button: open preview tab/modal showing learner-facing view.
  - Version history sidebar: list versions, allow restore (confirm before overwriting).
  - Collections management: choose which collections contain this course.
- Block editor HTMX patterns:
  - Text: `<textarea>` with HTML editor or simple textarea; autosave on change.
  - Image: file input with drag-drop; show upload progress; replace with image preview on success.
  - Video: file input with drag-drop; show upload progress to bunny.net; display video preview player on success.
  - File: file input; show uploaded filename and download link.
  - Quiz: form for adding/editing questions; drag-reorder questions; delete question confirm.
- Error handling: show toast/alert on save failure; retry logic on connection loss.

### Permission Enforcement

- All authoring endpoints MUST check permission via Task 3's permission middleware: only `teacher` or `org_owner` roles can POST/PATCH/DELETE courses, chapters, lessons, blocks, assets, or publish/unpublish. Endpoint must check `app.user_role` (from context, set by middleware).
- Reject with 403 if user's role is `learner` or if user not in org (org_id mismatch).
- RLS on all tables automatically enforces org isolation; Go-side permission check is defense-in-depth for role-based access.

### Summary of Deliverables

1. Postgres migration: schema for courses, chapters, lessons, blocks (with 5 types), assets, categories, tags, versions, prerequisites, completion_rules, collections, junction tables, with RLS policies on each.
2. Go/Gin models and repository functions for CRUD on all entities.
3. API endpoints (JSON) for course lifecycle, chapters, lessons, blocks, assets, versions, collections, media upload (signed URLs).
4. HTMX server-rendered course editor UI with autosave, publish, preview, version history, block editor.
5. Media upload handlers: signed URL generation for bunny.net (video) and Supabase Storage (images/files).
6. Access control enforcement: role-based checks in Go, RLS in Postgres.
7. Tests: unit tests for core logic (status transitions, sorting, duplication), integration tests for API endpoints, integration tests for media upload flow.

## Acceptance criteria

- [ ] A teacher can create a course, add chapters and lessons, author blocks (text, image, video, file, quiz) without direct database access, all through the HTMX UI or JSON API.
- [ ] Course status lifecycle (draft → review → published/scheduled/unpublished → archived) is enforced server-side; transitions not in the documented flow are rejected.
- [ ] Draft, review, scheduled, and unpublished courses are not visible or queryable by learners; only `published` courses with a past publish_date are visible to learners.
- [ ] Video files uploaded by teachers are stored in bunny.net (outside the container) and accessed only via signed URLs generated by the backend.
- [ ] Images and other file uploads are stored in Supabase Storage (outside the container) and accessed only via signed URLs generated by the backend.
- [ ] Signed URLs for private/draft course assets expire within 5 minutes (or shorter); published course assets may have longer expiry (up to 1 hour) but are revoked upon unpublish.
- [ ] Teacher can autosave block content in-progress (changing `updated_at` but not `published_at`) and explicitly publish (changing course status to `published`, setting `published_at`, creating version snapshot).
- [ ] Teacher can preview a draft/review/scheduled course before publishing (preview does not require publish_date to be in past).
- [ ] Chapter/lesson/block ordering survives page reloads and concurrent edits (no silent data loss); fractional sort_order scheme supports drag-reorder without conflicts.
- [ ] Course duplication creates an independent copy with new IDs for course/chapters/lessons/blocks, starting in `draft` status.
- [ ] Version history captures full course snapshot on publish; teacher can view prior versions and restore to prior version (creates new version, does not overwrite).
- [ ] Collections can group multiple courses; courses can belong to zero or more collections; collection order is preserved.
- [ ] Only users with `teacher` or `org_owner` role (verified via Task 3 middleware) can author, edit, delete, or publish courses; `learner` users are rejected with 403.
- [ ] All course-domain tables have `org_id` column and Postgres RLS policies enforcing org isolation; Go code does not need to filter by org_id (RLS is the boundary).
- [ ] Block types are limited to exactly: text (HTML), image (asset ref + alt text), video (asset ref + duration), file (asset ref + filename), quiz (questions array for authoring, no scoring).
- [ ] No learner-facing playback, progress tracking, quiz-taking/scoring, or certificate logic is implemented in this task; boundary ends at course content authoring and publishing.

## Commit convention

Your commit message MUST include `Closes #<issue-number>` (issue number to be filled in when published to GitHub) when the task's GitHub issue closes.
