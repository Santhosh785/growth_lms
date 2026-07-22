// Package httpserver wires together the Gin engine: middleware, CORS,
// proxy trust, and routes.
package httpserver

import (
	"log/slog"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"growth-lms/internal/auth"
	"growth-lms/internal/config"
	"growth-lms/internal/httpserver/handlers"
	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/httpserver/webconsole"
	"growth-lms/internal/media"
	"growth-lms/internal/models"
	"growth-lms/internal/ratelimit"
	"growth-lms/internal/worker"
)

// New builds the Gin engine for the application: request ID and logging
// middleware, CORS policy from config, proxy trust settings, health/
// readiness routes, and the Task 3 auth/organization/tenancy routes.
func New(cfg *config.Config, logger *slog.Logger, db *pgxpool.Pool, redisClient *redis.Client) *gin.Engine {
	if cfg.Env == config.EnvProduction {
		gin.SetMode(gin.ReleaseMode)
	}

	engine := gin.New()
	engine.Use(gin.Recovery())

	if cfg.TrustProxy {
		// Proxy count of 1 assumes a single nginx hop in front of the app,
		// matching the deployment topology described in Task 2.
		_ = engine.SetTrustedProxies([]string{"0.0.0.0/0"})
	} else {
		_ = engine.SetTrustedProxies(nil)
	}

	engine.Use(middleware.RequestID())
	engine.Use(middleware.RequestLogger(logger))
	engine.Use(corsMiddleware(cfg))

	engine.GET("/healthz", handlers.Healthz)
	engine.GET("/readyz", handlers.Readyz(db, redisClient))

	if cfg.Env == config.EnvDevelopment {
		// Manual-testing-only console for the JSON API; never mounted outside development.
		engine.GET("/test-console", func(c *gin.Context) {
			c.Data(200, "text/html; charset=utf-8", webconsole.IndexHTML)
		})
	}

	verifier, err := auth.NewVerifier(cfg.Supabase)
	if err != nil {
		// Config validation doesn't check the JWT secret's shape beyond
		// non-empty, and NewVerifier only fails on empty — this can only
		// happen if LMS_SUPABASE_JWT_SECRET was blanked out after Load()
		// already required it, which config.Load() already prevents.
		panic(err)
	}

	redisOpt, err := asynq.ParseRedisURI(cfg.Redis.URL)
	if err != nil {
		// config.Load() already validated LMS_REDIS_URL is a well-formed
		// URL; this can only fail if it was blanked out afterward.
		panic(err)
	}
	asyncQueue := worker.NewClient(redisOpt)

	deps := &handlers.AuthDeps{
		Config:      cfg,
		Pool:        db,
		Redis:       redisClient,
		Verifier:    verifier,
		Supabase:    auth.NewSupabaseClient(cfg.Supabase),
		Profiles:    models.NewProfileRepo(),
		Orgs:        models.NewOrgRepo(),
		Memberships: models.NewMembershipRepo(),
		Invitations: models.NewInvitationRepo(),
		Audit:       models.NewAuditRepo(),
		APITokens:   models.NewAPITokenRepo(),

		Courses:         models.NewCourseRepo(),
		Chapters:        models.NewChapterRepo(),
		Lessons:         models.NewLessonRepo(),
		Blocks:          models.NewBlockRepo(),
		Assets:          models.NewAssetRepo(),
		Categories:      models.NewCategoryRepo(),
		Tags:            models.NewTagRepo(),
		Collections:     models.NewCollectionRepo(),
		CourseVersions:  models.NewCourseVersionRepo(),
		CoursePrereqs:   models.NewCoursePrerequisiteRepo(),
		CompletionRules: models.NewCourseCompletionRuleRepo(),
		Bunny:           media.NewBunnyClient(cfg.BunnyNet),
		Storage:         media.NewStorageClient(cfg.Supabase),
		AsyncQueue:      asyncQueue,

		LearnerCourseAccess:   models.NewLearnerCourseAccessRepo(),
		ResumePositions:       models.NewLearnerResumePositionRepo(),
		LearnerProgress:       models.NewLearnerLessonProgressRepo(),
		Certificates:          models.NewLearnerCertificateRepo(),
		QuizAttempts:          models.NewLearnerQuizAttemptRepo(),
		QuizScores:            models.NewLearnerQuizScoreRepo(),
		AssignmentSubmissions: models.NewLearnerAssignmentSubmissionRepo(),
		AssignmentGrades:      models.NewLearnerAssignmentGradeRepo(),
		Announcements:         models.NewCourseAnnouncementRepo(),
	}

	// Public landing page: OptionalAuthenticate resolves the session
	// cookie if present without aborting, so HomePage can redirect an
	// already-logged-in visitor to /dashboard while still serving the
	// login/signup page to everyone else.
	engine.GET("/", middleware.OptionalAuthenticate(verifier), handlers.HomePage(deps))

	registerAuthRoutes(engine, deps, redisClient)
	registerOrgRoutes(engine, deps, db)
	registerCourseRoutes(engine, deps, db)
	registerLearnerRoutes(engine, deps, db)
	registerLearnerUIRoutes(engine, deps, db)

	// PUBLIC, unauthenticated certificate verification (Task 5 Stage 6):
	// mounted directly on the engine, no Authenticate/WithRequestTx at
	// all — see handlers.VerifyCertificate's doc comment for why this is
	// safe (a SECURITY DEFINER function hard-limits what can ever be
	// returned, not RLS session context this request doesn't have).
	// Stage 8: this single route now content-negotiates HTML vs JSON on
	// the Accept header (see the handler's doc comment) rather than
	// being split into two routes.
	engine.GET("/certificates/verify/:certificateId", handlers.VerifyCertificate(deps))

	return engine
}

// registerAuthRoutes mounts /api/auth/*: registration, email verification,
// login/logout, password reset, and account deletion. Login, register,
// and password-reset-request are additionally protected by a 5-per-15-min
// per-IP rate limit; Login layers a per-email exponential backoff on top
// (see handlers.Login).
func registerAuthRoutes(engine *gin.Engine, d *handlers.AuthDeps, redisClient *redis.Client) {
	authLimit := func(prefix string) gin.HandlerFunc {
		limiter := ratelimit.New(redisClient, "ratelimit:"+prefix, 5, 15*time.Minute)
		return middleware.RateLimit(limiter, middleware.ByClientIP)
	}

	api := engine.Group("/api/auth")
	api.POST("/register", authLimit("auth-register"), handlers.Register(d))
	api.POST("/verify-email", handlers.VerifyEmail(d))
	api.POST("/login", authLimit("auth-login"), handlers.Login(d))
	api.POST("/password-reset-request", authLimit("auth-reset"), handlers.PasswordResetRequest(d))
	api.POST("/password-reset", handlers.PasswordReset(d))

	authed := api.Group("")
	authed.Use(middleware.Authenticate(d.Verifier))
	authed.POST("/logout", handlers.Logout(d))
	authed.DELETE("/delete-account", handlers.DeleteAccount(d))
}

// registerOrgRoutes mounts /api/orgs/* and /api/invitations/*: everything
// requires authentication and a request-scoped transaction so RLS session
// variables are in effect; org-scoped routes additionally resolve
// membership/role via ResolveOrg and gate mutations with RequireRole.
func registerOrgRoutes(engine *gin.Engine, d *handlers.AuthDeps, db *pgxpool.Pool) {
	authed := engine.Group("/api")
	authed.Use(middleware.Authenticate(d.Verifier))
	authed.Use(middleware.WithRequestTx(db))

	authed.POST("/orgs", handlers.CreateOrg(d))

	org := authed.Group("/orgs/:org_slug")
	org.Use(middleware.ResolveOrg(d.Orgs, d.Memberships, d.Profiles))

	org.GET("", handlers.GetOrg(d))
	org.PATCH("", middleware.RequireRole(auth.RoleOwner), handlers.UpdateOrg(d))
	org.DELETE("", middleware.RequireRole(auth.RoleOwner), handlers.DeleteOrg(d))

	org.GET("/members", handlers.ListMembers(d))
	org.PATCH("/members/:user_id", middleware.RequireRole(auth.RoleOwner), handlers.ChangeMemberRole(d))
	org.DELETE("/members/:user_id", middleware.RequireRole(auth.RoleOwner), handlers.RemoveMember(d))

	org.POST("/invitations", middleware.RequireRole(auth.RoleOwner), handlers.CreateInvitation(d))
	org.GET("/invitations", middleware.RequireRole(auth.RoleOwner), handlers.ListInvitations(d))
	org.DELETE("/invitations/:invitation_id", middleware.RequireRole(auth.RoleOwner), handlers.RevokeInvitation(d))

	org.POST("/api-tokens", middleware.RequireRole(auth.RoleOwner), handlers.CreateAPIToken(d))
	org.GET("/api-tokens", middleware.RequireRole(auth.RoleOwner), handlers.ListAPITokens(d))
	org.DELETE("/api-tokens/:token_id", middleware.RequireRole(auth.RoleOwner), handlers.RevokeAPIToken(d))

	// Invitation accept/decline are keyed by token, not by org slug: the
	// caller isn't a member of the target org yet, so there's no org
	// context to resolve.
	authed.POST("/invitations/:token/accept", handlers.AcceptInvitation(d))
	authed.POST("/invitations/:token/decline", handlers.DeclineInvitation(d))
}

// registerCourseRoutes mounts Task 4's course-domain routes. Course-scoped
// endpoints are flat (/api/courses/:courseId/...) and resolve org context
// via ResolveCourseOrg rather than an :org_slug path segment (see
// plans/task-4-implementation/main-plan.md's Q7). categories/collections
// have no course to derive org context from, so they nest under
// /api/orgs/:org_slug/... reusing the existing ResolveOrg group instead —
// a deliberate, noted exception to the flat-path convention.
func registerCourseRoutes(engine *gin.Engine, d *handlers.AuthDeps, db *pgxpool.Pool) {
	authoring := middleware.RequireRole(auth.RoleOwner, auth.RoleTeacher)

	authed := engine.Group("/api")
	authed.Use(middleware.Authenticate(d.Verifier))
	authed.Use(middleware.WithRequestTx(db))

	// CreateCourse/ListCourses have no :courseId in their path to derive
	// org context from (there's no course yet / the caller specifies
	// which org via org_slug in the request) — they resolve org and
	// role-check internally rather than via RequireRole, see
	// resolveOrgBySlugForCourseCreation in handlers/courses.go.
	authed.POST("/courses", handlers.CreateCourse(d))
	authed.GET("/courses", handlers.ListCourses(d))

	course := authed.Group("/courses/:courseId")
	course.Use(middleware.ResolveCourseOrg(d.Courses, d.Memberships, d.Profiles))

	course.GET("", authoring, handlers.GetCourse(d))
	course.PATCH("", authoring, handlers.UpdateCourse(d))
	course.DELETE("", authoring, handlers.DeleteCourse(d))
	course.POST("/transition", authoring, handlers.TransitionCourse(d))
	course.POST("/publish", authoring, handlers.PublishCourse(d))
	course.POST("/unpublish", authoring, handlers.UnpublishCourse(d))
	course.POST("/duplicate", authoring, handlers.DuplicateCourse(d))
	course.GET("/preview", authoring, handlers.PreviewCourse(d))

	course.POST("/tags", authoring, handlers.AddTagToCourse(d))
	course.DELETE("/tags/:tagId", authoring, handlers.RemoveTagFromCourse(d))
	course.GET("/tags", authoring, handlers.ListCourseTags(d))

	course.GET("/versions", authoring, handlers.ListCourseVersions(d))
	course.GET("/versions/:versionId", authoring, handlers.GetCourseVersion(d))
	course.POST("/versions/:versionId/restore", authoring, handlers.RestoreCourseVersion(d))

	course.POST("/chapters", authoring, handlers.CreateChapter(d))
	course.GET("/chapters", authoring, handlers.ListChapters(d))
	course.POST("/chapters/reorder", authoring, handlers.ReorderChapters(d))
	course.PATCH("/chapters/:chapterId", authoring, handlers.UpdateChapter(d))
	course.DELETE("/chapters/:chapterId", authoring, handlers.DeleteChapter(d))

	course.POST("/chapters/:chapterId/lessons", authoring, handlers.CreateLesson(d))
	course.GET("/chapters/:chapterId/lessons", authoring, handlers.ListLessons(d))
	course.POST("/chapters/:chapterId/lessons/reorder", authoring, handlers.ReorderLessons(d))
	course.PATCH("/chapters/:chapterId/lessons/:lessonId", authoring, handlers.UpdateLesson(d))
	course.DELETE("/chapters/:chapterId/lessons/:lessonId", authoring, handlers.DeleteLesson(d))

	course.POST("/chapters/:chapterId/lessons/:lessonId/blocks", authoring, handlers.CreateBlock(d))
	course.GET("/chapters/:chapterId/lessons/:lessonId/blocks", authoring, handlers.ListBlocks(d))
	course.POST("/chapters/:chapterId/lessons/:lessonId/blocks/reorder", authoring, handlers.ReorderBlocks(d))
	course.PATCH("/chapters/:chapterId/lessons/:lessonId/blocks/:blockId", authoring, handlers.UpdateBlock(d))
	course.POST("/chapters/:chapterId/lessons/:lessonId/blocks/:blockId/autosave", authoring, handlers.AutosaveBlock(d))
	course.DELETE("/chapters/:chapterId/lessons/:lessonId/blocks/:blockId", authoring, handlers.DeleteBlock(d))

	course.POST("/media/upload/video", authoring, handlers.UploadVideo(d))
	course.POST("/media/upload", authoring, handlers.UploadFile(d))
	course.POST("/media/upload/:pendingId/complete", authoring, handlers.UploadFileComplete(d))
	course.PATCH("/assets/:assetId/refresh-url", authoring, handlers.RefreshAssetURL(d))

	// Task 5 Stage 5: teacher-side assignment grading, wired into this same
	// authoring-gated course group (not registerLearnerRoutes) since
	// grading is an authoring action, not a learner one.
	course.GET("/submissions", authoring, handlers.ListCourseSubmissions(d))
	course.POST("/submissions/:submissionId/grade", authoring, handlers.GradeSubmission(d))

	// Task 5 Stage 7: teacher-authored announcements. Creation is an
	// authoring action (wired here, not registerLearnerRoutes); reading is
	// learner-facing (wired in registerLearnerRoutes, RequireEntitlement-
	// gated) since any enrolled learner needs to read them, not just
	// owner/teacher.
	course.POST("/announcements", authoring, handlers.CreateAnnouncement(d))

	// Categories/collections have no course in their path — mounted under
	// the org-slug group instead, per the noted exception above.
	org := authed.Group("/orgs/:org_slug")
	org.Use(middleware.ResolveOrg(d.Orgs, d.Memberships, d.Profiles))

	org.POST("/categories", middleware.RequireRole(auth.RoleOwner), handlers.CreateCategory(d))
	org.GET("/categories", handlers.ListCategories(d))
	org.PATCH("/categories/:categoryId", middleware.RequireRole(auth.RoleOwner), handlers.UpdateCategory(d))
	org.DELETE("/categories/:categoryId", middleware.RequireRole(auth.RoleOwner), handlers.DeleteCategory(d))

	org.POST("/collections", authoring, handlers.CreateCollection(d))
	org.GET("/collections", handlers.ListCollections(d))
	org.PATCH("/collections/:collectionId", authoring, handlers.UpdateCollection(d))
	org.DELETE("/collections/:collectionId", authoring, handlers.DeleteCollection(d))
	org.POST("/collections/:collectionId/courses", authoring, handlers.AddCourseToCollection(d))
	org.GET("/collections/:collectionId/courses", handlers.ListCollectionCourses(d))
	org.DELETE("/collections/:collectionId/courses/:courseId", authoring, handlers.RemoveCourseFromCollection(d))
	org.POST("/collections/:collectionId/courses/reorder", authoring, handlers.ReorderCollectionCourses(d))

	// The Bunny transcode-complete webhook has NO auth/RLS middleware at
	// all — it's an external caller with no session, no bearer token, no
	// org context. The handler itself verifies the HMAC signature before
	// doing anything, matching the "verified provider webhook only" rule
	// the spec compares to the payments-webhook precedent.
	engine.POST("/api/webhooks/bunny", handlers.BunnyWebhook(d))

	// Lightweight HTMX course-editor UI: cookie-authenticated (the same
	// session cookie the JSON API's Authenticate middleware already
	// accepts), CSRF-protected on every mutating route (see
	// middleware/csrf.go) — the first cookie-driven HTML mutation surface
	// in this codebase, closing the gap Task 3's grilling record flagged
	// (Q57) but never implemented.
	editorAuthed := engine.Group("")
	editorAuthed.Use(middleware.Authenticate(d.Verifier))
	editorAuthed.Use(middleware.WithRequestTx(db))
	editorAuthed.Use(middleware.EnsureCSRFCookie(d.Config))

	editor := editorAuthed.Group("/courses/:courseId/edit")
	editor.Use(middleware.ResolveCourseOrg(d.Courses, d.Memberships, d.Profiles))
	editor.Use(middleware.RequireRole(auth.RoleOwner, auth.RoleTeacher))

	editor.GET("", handlers.CourseEditorPage(d))
	editor.POST("/chapters", middleware.RequireCSRF(), handlers.CourseEditorCreateChapter(d))
	editor.POST("/chapters/:chapterId/move", middleware.RequireCSRF(), handlers.CourseEditorMoveChapter(d))
	editor.POST("/chapters/:chapterId/lessons", middleware.RequireCSRF(), handlers.CourseEditorCreateLesson(d))
	editor.POST("/chapters/:chapterId/lessons/:lessonId/blocks", middleware.RequireCSRF(), handlers.CourseEditorCreateBlock(d))
	editor.POST("/blocks/:blockId/autosave", middleware.RequireCSRF(), handlers.CourseEditorAutosaveBlock(d))
	editor.POST("/transition", middleware.RequireCSRF(), handlers.CourseEditorTransition(d))
	editor.POST("/publish", middleware.RequireCSRF(), handlers.CourseEditorPublish(d))
	editor.POST("/unpublish", middleware.RequireCSRF(), handlers.CourseEditorUnpublish(d))
	editor.POST("/versions/:versionId/restore", middleware.RequireCSRF(), handlers.CourseEditorRestoreVersion(d))
}

// registerLearnerRoutes mounts Task 5's learner-journey routes, reusing the
// same flat /api/courses/:courseId/... convention and ResolveCourseOrg
// middleware registerCourseRoutes uses (a separate Gin route group, since
// registerCourseRoutes' own "course" group is local to that function, but
// the same underlying middleware and org-resolution semantics).
//
// Enroll is deliberately NOT gated by RequireRole or RequireEntitlement —
// any org member may self-enroll in a published course (ResolveCourseOrg
// itself already 404s non-members/non-platform-owners, so "any org
// member" is exactly what reaches the handler). Every other route here
// needs an active learner_course_access row (or owner/teacher/platform-
// owner status), enforced by RequireEntitlement.
func registerLearnerRoutes(engine *gin.Engine, d *handlers.AuthDeps, db *pgxpool.Pool) {
	authed := engine.Group("/api")
	authed.Use(middleware.Authenticate(d.Verifier))
	authed.Use(middleware.WithRequestTx(db))

	// GET /api/certificates lists the caller's own certificates
	// (ListByLearner is already learner-scoped by RLS + the query itself)
	// — it needs only authentication, not RequireEntitlement (which is
	// course-scoped and doesn't apply to a cross-course listing) or an
	// :courseId in the path at all.
	authed.GET("/certificates", handlers.ListCertificates(d))

	course := authed.Group("/courses/:courseId")
	course.Use(middleware.ResolveCourseOrg(d.Courses, d.Memberships, d.Profiles))

	course.POST("/enroll", handlers.EnrollCourse(d))

	entitled := middleware.RequireEntitlement(d.LearnerCourseAccess)
	course.GET("/player", entitled, handlers.GetPlayer(d))
	course.POST("/lessons/:lessonId/resume", entitled, handlers.ResumeLesson(d))
	course.POST("/lessons/:lessonId/progress", entitled, handlers.ReportLessonProgress(d))
	course.POST("/lessons/:lessonId/complete", entitled, handlers.CompleteLesson(d))
	course.GET("/progress", entitled, handlers.GetCourseProgress(d))

	course.GET("/lessons/:lessonId/blocks/:blockId/quiz", entitled, handlers.GetQuiz(d))
	course.POST("/lessons/:lessonId/blocks/:blockId/quiz/submit", entitled, handlers.SubmitQuiz(d))

	course.POST("/lessons/:lessonId/blocks/:blockId/assignment/upload", entitled, handlers.UploadAssignmentSubmission(d))
	course.POST("/lessons/:lessonId/blocks/:blockId/assignment/submit", entitled, handlers.SubmitAssignment(d))
	course.GET("/lessons/:lessonId/blocks/:blockId/assignment/submissions", entitled, handlers.GetAssignmentSubmissions(d))

	// Task 5 Stage 6: the learner's own certificate for this course, if
	// issued.
	course.GET("/certificate", entitled, handlers.GetCourseCertificate(d))

	// Task 5 Stage 7: reading announcements is learner-facing (creation is
	// authoring-gated, see registerCourseRoutes).
	course.GET("/announcements", entitled, handlers.ListAnnouncements(d))
}

// registerLearnerUIRoutes mounts Task 5 Stage 8's lightweight
// server-rendered learner-facing pages: cookie-authenticated (same
// session cookie as the course-editor UI, via Authenticate accepting
// either a bearer header or the lms_session cookie), reusing
// ResolveCourseOrg/RequireEntitlement exactly as registerLearnerRoutes'
// JSON routes do. No CSRF middleware here: unlike the course-editor UI,
// none of these pages POST/PATCH/DELETE directly — every mutation is a
// small inline fetch() call to the existing JSON API (registerLearnerRoutes/
// registerCourseRoutes), which itself carries no CSRF protection (see
// middleware/csrf.go's doc comment), so there is nothing for these GET-only
// page routes to protect.
func registerLearnerUIRoutes(engine *gin.Engine, d *handlers.AuthDeps, db *pgxpool.Pool) {
	authed := engine.Group("")
	authed.Use(middleware.Authenticate(d.Verifier))
	authed.Use(middleware.WithRequestTx(db))

	// No course in the path to resolve org context from — every table
	// this page queries is scoped by learner_id = app_current_user_id()
	// at the RLS layer (see handlers.LearnerDashboardPage's doc comment),
	// matching ListCertificates' own precedent.
	authed.GET("/dashboard", handlers.LearnerDashboardPage(d))

	course := authed.Group("/courses/:courseId")
	course.Use(middleware.ResolveCourseOrg(d.Courses, d.Memberships, d.Profiles))

	// Deliberately NOT RequireEntitlement: a non-enrolled org member must
	// still be able to load the landing page to see an Enroll button.
	course.GET("/learn", handlers.CourseLearnPage(d))

	entitled := middleware.RequireEntitlement(d.LearnerCourseAccess)
	course.GET("/learn/lessons/:lessonId", entitled, handlers.LessonPlayerPage(d))

	course.GET("/submissions", middleware.RequireRole(auth.RoleOwner, auth.RoleTeacher), handlers.CourseSubmissionsPage(d))
}

func corsMiddleware(cfg *config.Config) gin.HandlerFunc {
	corsCfg := cors.Config{
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", middleware.RequestIDHeader},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}

	if cfg.Env == config.EnvDevelopment && len(cfg.CORS.AllowedOrigins) == 0 {
		corsCfg.AllowOriginFunc = func(origin string) bool { return true }
	} else {
		corsCfg.AllowOrigins = cfg.CORS.AllowedOrigins
	}

	return cors.New(corsCfg)
}
