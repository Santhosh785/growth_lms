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

	"growth-lms/internal/ai"
	"growth-lms/internal/auth"
	"growth-lms/internal/codeexec"
	"growth-lms/internal/config"
	"growth-lms/internal/httpserver/handlers"
	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/httpserver/webconsole"
	"growth-lms/internal/media"
	"growth-lms/internal/models"
	"growth-lms/internal/payments"
	"growth-lms/internal/ratelimit"
	"growth-lms/internal/realtime"
	"growth-lms/internal/scorm"
	"growth-lms/internal/simulations"
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

		Payments:      payments.NewRazorpayProvider(cfg.Razorpay),
		WebhookEvents: models.NewWebhookEventRepo(),

		// Orders/Entitlements/PlatformSettings back the admin-dashboard
		// routes (registerAdminUIRoutes below). The remaining Task 6
		// commerce repos (Offers, DiscountCodes, InviteTokens,
		// CommercePayments, Refunds, PaymentAuditTrail) back
		// registerCommerceRoutes below — task-10's routes-wiring step.
		Orders:           models.NewOrderRepo(),
		Entitlements:     models.NewEntitlementRepo(),
		PlatformSettings: models.NewPlatformSettingsRepo(),

		Offers:            models.NewOfferRepo(),
		DiscountCodes:     models.NewDiscountCodeRepo(),
		InviteTokens:      models.NewInviteTokenRepo(),
		CommercePayments:  models.NewPaymentRepo(),
		Refunds:           models.NewRefundRepo(),
		PaymentAuditTrail: models.NewPaymentAuditRepo(),

		Threads:           models.NewDiscussionThreadRepo(),
		Posts:             models.NewDiscussionPostRepo(),
		Reactions:         models.NewPostReactionRepo(),
		Mentions:          models.NewPostMentionRepo(),
		Reports:           models.NewContentReportRepo(),
		Notifications:     models.NewNotificationRepo(),
		NotificationPrefs: models.NewNotificationPreferenceRepo(),
		UnsubscribeTokens: models.NewUnsubscribeTokenRepo(),
		Boards:            models.NewCollabBoardRepo(),
		BoardVersions:     models.NewCollabBoardVersionRepo(),
		BoardTemplates:    models.NewCollabBoardTemplateRepo(),

		AnalyticsEvents:  models.NewAnalyticsEventRepo(),
		AnalyticsRollups: models.NewAnalyticsRollupRepo(),
		OrgPages:         models.NewOrgPageRepo(),
		Search:           models.NewSearchRepo(),

		AI:            ai.NewServiceFromSettings(cfg.AI.Enabled, cfg.AI.Provider, cfg.AI.APIKey, cfg.AI.Model),
		AIGenerations: models.NewAIGenerationRepo(),
		AIUsage:       models.NewAIUsageRepo(),
		AITutor:       models.NewAITutorRepo(),

		PodcastShows:     models.NewPodcastShowRepo(),
		PodcastEpisodes:  models.NewPodcastEpisodeRepo(),
		PodcastPlaylists: models.NewPodcastPlaylistRepo(),
		PodcastProgress:  models.NewPodcastProgressRepo(),

		CodeExec: codeexec.NewServiceFromSettings(cfg.CodeExec.Enabled, cfg.CodeExec.Runner, codeexec.Limits{
			CPUMillis:      cfg.CodeExec.DefaultCPUMillis,
			MemoryBytes:    cfg.CodeExec.DefaultMemoryBytes,
			WallTimeMillis: cfg.CodeExec.DefaultWallMillis,
			MaxOutputBytes: cfg.CodeExec.DefaultMaxOutputByte,
		}),
		CodeExercises:   models.NewCodeExerciseRepo(),
		CodeSubmissions: models.NewCodeSubmissionRepo(),
		CodeExecUsage:   models.NewCodeExecUsageRepo(),

		Scorm:         scorm.NewService(),
		ScormPackages: models.NewScormPackageRepo(),
		ScormAttempts: models.NewScormAttemptRepo(),

		Simulations:        simulations.NewService(cfg.Simulations.MaxSourceBytes, cfg.Simulations.MaxParameters),
		SimulationRepo:     models.NewSimulationRepo(),
		SimulationProgress: models.NewSimulationProgressRepo(),
	}

	// Public landing page: OptionalAuthenticate resolves the session
	// cookie if present without aborting, so HomePage can redirect an
	// already-logged-in visitor to /dashboard while still serving the
	// login/signup page to everyone else.
	engine.GET("/", middleware.OptionalAuthenticate(verifier), handlers.HomePage(deps))

	// Shared nav bar every server-rendered page htmx-loads on load (see
	// templates/nav.html + handlers.NavFragment's doc comment): resolves
	// its own auth/role state, so pages don't thread that data through
	// their own handlers just to render a nav. WithRequestTx is needed
	// because NavFragment queries profiles/memberships for role-based
	// links; OptionalAuthenticate (not Authenticate) so the fragment
	// still renders a "Log in" link for anonymous visitors instead of 401ing.
	engine.GET("/nav", middleware.OptionalAuthenticate(verifier), middleware.WithRequestTx(db), handlers.NavFragment(deps))
	engine.GET("/nav/logout", middleware.Authenticate(verifier), handlers.NavLogoutRedirect(deps))

	registerAuthRoutes(engine, deps, db, redisClient)
	registerOrgRoutes(engine, deps, db)
	registerCourseRoutes(engine, deps, db, redisClient)
	registerLearnerRoutes(engine, deps, db)
	registerLearnerUIRoutes(engine, deps, db)
	registerCommerceRoutes(engine, deps, db, redisClient)
	registerAdminUIRoutes(engine, deps, db)
	registerCommunityRoutes(engine, deps, db)
	registerGrowthRoutes(engine, deps, db)
	registerAIRoutes(engine, deps, db)
	registerPodcastRoutes(engine, deps, db)
	registerCodeExecRoutes(engine, deps, db)
	registerScormRoutes(engine, deps, db)
	registerSimulationsRoutes(engine, deps, db)
	registerPublicSiteRoutes(engine, deps)

	// Task 7 in-process realtime hub: presence + collaborative board ops.
	// Board ops are debounce-persisted to collab_boards.snapshot via the
	// coordinator wired here as the hub's message callback.
	hub := realtime.NewHub()
	hub.SetOnMessage(handlers.NewBoardCoordinator(db, deps.Boards).OnMessage)
	registerRealtimeRoutes(engine, deps, hub)
	registerCommunityUIRoutes(engine, deps, db)

	// PUBLIC, unauthenticated one-click unsubscribe (Task 7): resolved via
	// the resolve_unsubscribe SECURITY DEFINER function against the pool, so
	// no Authenticate/WithRequestTx — same public-route pattern as
	// certificate verification below. GET confirms, POST applies.
	engine.GET("/unsubscribe/:token", handlers.UnsubscribePage(deps))
	engine.POST("/unsubscribe/:token", handlers.Unsubscribe(deps))

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
func registerAuthRoutes(engine *gin.Engine, d *handlers.AuthDeps, db *pgxpool.Pool, redisClient *redis.Client) {
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

	// admin-register creates a verified user via Supabase's Admin API,
	// bypassing email confirmation and the public signup rate limit — a
	// privileged provisioning action, so it is gated behind platform-owner
	// authentication (Authenticate + WithRequestTx + RequirePlatformOwner,
	// same chain as the /admin/organizations dashboards) rather than left
	// open. A per-IP rate limit is layered on as defence in depth.
	adminAuthed := api.Group("")
	adminAuthed.Use(middleware.Authenticate(d.Verifier))
	adminAuthed.Use(middleware.WithRequestTx(db))
	adminAuthed.POST("/admin-register", authLimit("auth-admin-register"),
		middleware.RequirePlatformOwner(d.Profiles), handlers.AdminRegister(d))
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

// registerGrowthRoutes mounts Task 8's authed, org-scoped routes:
// analytics, search, branding/theme settings, the landing-page builder,
// and custom-domain management. Reuses the exact ResolveOrg/RequireRole
// pattern registerOrgRoutes establishes above.
func registerGrowthRoutes(engine *gin.Engine, d *handlers.AuthDeps, db *pgxpool.Pool) {
	authed := engine.Group("/api")
	authed.Use(middleware.Authenticate(d.Verifier))
	authed.Use(middleware.WithRequestTx(db))

	org := authed.Group("/orgs/:org_slug")
	org.Use(middleware.ResolveOrg(d.Orgs, d.Memberships, d.Profiles))

	org.GET("/analytics", middleware.RequireRole(auth.RoleOwner, auth.RoleTeacher), handlers.OrgAnalytics(d))
	org.GET("/search", handlers.Search(d))

	org.GET("/branding", middleware.RequireRole(auth.RoleOwner), handlers.GetBranding(d))
	org.PATCH("/branding", middleware.RequireRole(auth.RoleOwner), handlers.UpdateBranding(d))

	org.POST("/domain", middleware.RequireRole(auth.RoleOwner), handlers.SetCustomDomain(d))
	org.POST("/domain/verify", middleware.RequireRole(auth.RoleOwner), handlers.VerifyDomain(d))

	org.GET("/pages", middleware.RequireRole(auth.RoleOwner, auth.RoleTeacher), handlers.ListOrgPages(d))
	org.PUT("/pages/:slug", middleware.RequireRole(auth.RoleOwner, auth.RoleTeacher), handlers.UpsertOrgPage(d))
	org.DELETE("/pages/:slug", middleware.RequireRole(auth.RoleOwner, auth.RoleTeacher), handlers.DeleteOrgPage(d))

	course := authed.Group("/courses/:courseId")
	course.Use(middleware.ResolveCourseOrg(d.Courses, d.Memberships, d.Profiles))
	course.GET("/offers/:offerId/embed-link", handlers.CourseCheckoutLink(d))
}

// registerAIRoutes mounts Task 9's AI authoring & tutor routes. Authoring
// (outline/lesson/quiz generation) is owner/teacher-gated and course-scoped,
// reusing the ResolveCourseOrg + RequireRole pattern registerCourseRoutes
// establishes. The tutor is learner-facing and entitlement-gated (any
// enrolled learner, plus owner/teacher, per RequireEntitlement). AI settings
// (owner) and the usage dashboard (owner/teacher) are org-scoped. Every
// generation handler independently enforces the AI feature flag and the
// monthly usage cap (see handlers.generateGated) — the route gating here is
// authz only, not the feature/limit gate.
func registerAIRoutes(engine *gin.Engine, d *handlers.AuthDeps, db *pgxpool.Pool) {
	authoring := middleware.RequireRole(auth.RoleOwner, auth.RoleTeacher)

	authed := engine.Group("/api")
	authed.Use(middleware.Authenticate(d.Verifier))
	authed.Use(middleware.WithRequestTx(db))

	course := authed.Group("/courses/:courseId")
	course.Use(middleware.ResolveCourseOrg(d.Courses, d.Memberships, d.Profiles))

	course.POST("/ai/outline", authoring, handlers.GenerateOutline(d))
	course.POST("/ai/lesson", authoring, handlers.GenerateLesson(d))
	course.POST("/ai/quiz", authoring, handlers.GenerateQuiz(d))

	entitled := middleware.RequireEntitlement(d.LearnerCourseAccess)
	course.POST("/ai/tutor", entitled, handlers.TutorAsk(d))
	course.GET("/ai/tutor/sessions", entitled, handlers.ListTutorSessions(d))
	course.GET("/ai/tutor/sessions/:sessionId", entitled, handlers.GetTutorSession(d))

	org := authed.Group("/orgs/:org_slug")
	org.Use(middleware.ResolveOrg(d.Orgs, d.Memberships, d.Profiles))
	org.GET("/ai/settings", middleware.RequireRole(auth.RoleOwner), handlers.GetAISettings(d))
	org.PATCH("/ai/settings", middleware.RequireRole(auth.RoleOwner), handlers.UpdateAISettings(d))
	org.GET("/ai/usage", middleware.RequireRole(auth.RoleOwner, auth.RoleTeacher), handlers.AIUsageDashboard(d))
}

// registerPodcastRoutes mounts Task 9's Podcasts & RSS authoring, settings,
// and learner-progress routes — all authed and org-scoped, reusing the exact
// ResolveOrg/RequireRole pattern registerOrgRoutes establishes. Authoring
// (show/episode/playlist CRUD, publish) is owner/teacher-gated; episode
// detail + listen-progress reporting are open to any authenticated org
// member (RLS scopes progress to the caller); settings are owner-only. Every
// handler independently enforces the two-flag feature gate (platform
// LMS_PODCASTS_ENABLED AND the org's podcasts_enabled) via handlers.podcastGate
// — the route gating here is authz only, not the feature gate. The PUBLIC RSS
// feed is registered in registerPublicSiteRoutes.
func registerPodcastRoutes(engine *gin.Engine, d *handlers.AuthDeps, db *pgxpool.Pool) {
	authoring := middleware.RequireRole(auth.RoleOwner, auth.RoleTeacher)

	authed := engine.Group("/api")
	authed.Use(middleware.Authenticate(d.Verifier))
	authed.Use(middleware.WithRequestTx(db))

	org := authed.Group("/orgs/:org_slug")
	org.Use(middleware.ResolveOrg(d.Orgs, d.Memberships, d.Profiles))

	org.GET("/podcasts/settings", middleware.RequireRole(auth.RoleOwner), handlers.GetPodcastSettings(d))
	org.PATCH("/podcasts/settings", middleware.RequireRole(auth.RoleOwner), handlers.UpdatePodcastSettings(d))

	// Shows + their episodes (owner/teacher authoring).
	org.POST("/podcasts/shows", authoring, handlers.CreatePodcastShow(d))
	org.GET("/podcasts/shows", authoring, handlers.ListPodcastShows(d))
	org.GET("/podcasts/shows/:showId", authoring, handlers.GetPodcastShow(d))
	org.PATCH("/podcasts/shows/:showId", authoring, handlers.UpdatePodcastShow(d))
	org.DELETE("/podcasts/shows/:showId", authoring, handlers.DeletePodcastShow(d))
	org.POST("/podcasts/shows/:showId/episodes", authoring, handlers.CreatePodcastEpisode(d))

	org.PATCH("/podcasts/episodes/:episodeId", authoring, handlers.UpdatePodcastEpisode(d))
	org.POST("/podcasts/episodes/:episodeId/publish", authoring, handlers.SetPodcastEpisodePublished(d))
	org.DELETE("/podcasts/episodes/:episodeId", authoring, handlers.DeletePodcastEpisode(d))

	// Episode detail + listen progress — any org member (RLS scopes progress
	// to the caller). Not authoring-gated: a learner needs to read the
	// episode/transcript and record their own listen position.
	org.GET("/podcasts/episodes/:episodeId", handlers.GetPodcastEpisodeDetail(d))
	org.POST("/podcasts/episodes/:episodeId/progress", handlers.ReportPodcastProgress(d))

	// Playlists (owner/teacher authoring).
	org.POST("/podcasts/playlists", authoring, handlers.CreatePodcastPlaylist(d))
	org.GET("/podcasts/playlists", authoring, handlers.ListPodcastPlaylists(d))
	org.GET("/podcasts/playlists/:playlistId", authoring, handlers.GetPodcastPlaylist(d))
	org.DELETE("/podcasts/playlists/:playlistId", authoring, handlers.DeletePodcastPlaylist(d))
	org.POST("/podcasts/playlists/:playlistId/items", authoring, handlers.AddPodcastPlaylistItem(d))
	org.DELETE("/podcasts/playlists/:playlistId/items/:episodeId", authoring, handlers.RemovePodcastPlaylistItem(d))
}

// registerCodeExecRoutes mounts Task 9's sandboxed code-execution routes — all
// authed and org-scoped, reusing the same ResolveOrg/RequireRole pattern.
// Exercise authoring (CRUD, publish) and the usage dashboard are owner/teacher
// gated; browsing published exercises, running ad-hoc code, submitting against
// an exercise, and reading one's own submissions are open to any authenticated
// org member (RLS scopes submissions to the caller); settings are owner-only.
// Every handler independently enforces the two-flag feature gate (platform
// LMS_CODE_EXEC_ENABLED AND the org's code_exec_enabled) via handlers'
// codeExecGate/executeGated — the route gating here is authz only, not the
// feature gate. There is no public/anonymous surface for this module.
func registerCodeExecRoutes(engine *gin.Engine, d *handlers.AuthDeps, db *pgxpool.Pool) {
	authoring := middleware.RequireRole(auth.RoleOwner, auth.RoleTeacher)

	authed := engine.Group("/api")
	authed.Use(middleware.Authenticate(d.Verifier))
	authed.Use(middleware.WithRequestTx(db))

	org := authed.Group("/orgs/:org_slug")
	org.Use(middleware.ResolveOrg(d.Orgs, d.Memberships, d.Profiles))

	// Settings + usage dashboard.
	org.GET("/code/settings", middleware.RequireRole(auth.RoleOwner), handlers.GetCodeExecSettings(d))
	org.PATCH("/code/settings", middleware.RequireRole(auth.RoleOwner), handlers.UpdateCodeExecSettings(d))
	org.GET("/code/usage", authoring, handlers.CodeExecUsageDashboard(d))

	// Exercise authoring (owner/teacher). GET returns full detail incl. the
	// solution and reference output — never exposed to learners.
	org.POST("/code/exercises", authoring, handlers.CreateCodeExercise(d))
	org.GET("/code/exercises", authoring, handlers.ListCodeExercises(d))
	org.GET("/code/exercises/:exerciseId", authoring, handlers.GetCodeExercise(d))
	org.PATCH("/code/exercises/:exerciseId", authoring, handlers.UpdateCodeExercise(d))
	org.POST("/code/exercises/:exerciseId/publish", authoring, handlers.SetCodeExercisePublished(d))
	org.DELETE("/code/exercises/:exerciseId", authoring, handlers.DeleteCodeExercise(d))

	// Learner-facing catalog (any member): published exercises, solutions
	// stripped. A distinct path from the authoring GETs so both can coexist.
	org.GET("/code/catalog", handlers.ListCodeCatalog(d))
	org.GET("/code/catalog/:exerciseId", handlers.GetCodeCatalogExercise(d))

	// Run + submit + own history (any member; RLS scopes submissions to the
	// caller). The submit path has an extra segment, so it does not collide
	// with the authoring GET on /code/exercises/:exerciseId.
	org.POST("/code/run", handlers.RunCode(d))
	org.POST("/code/exercises/:exerciseId/submit", handlers.SubmitCodeExercise(d))
	org.GET("/code/submissions", handlers.ListMyCodeSubmissions(d))
}

// registerScormRoutes mounts Task 9's SCORM 1.2/2004 routes — all authed and
// org-scoped, reusing the same ResolveOrg/RequireRole pattern. Package
// authoring (import/validate, edit, publish, delete) and per-package reporting
// are owner/teacher gated; browsing the published catalog, launching, and the
// SCO runtime (commit/finish) plus one's own attempt history are open to any
// authenticated org member (RLS scopes attempts to the caller); settings are
// owner-only. Every handler independently enforces the two-flag feature gate
// (platform LMS_SCORM_ENABLED AND the org's scorm_enabled) via scormGate — the
// route gating here is authz only. There is no public/anonymous surface: a SCO
// always runs inside an authenticated learner's session.
func registerScormRoutes(engine *gin.Engine, d *handlers.AuthDeps, db *pgxpool.Pool) {
	authoring := middleware.RequireRole(auth.RoleOwner, auth.RoleTeacher)

	authed := engine.Group("/api")
	authed.Use(middleware.Authenticate(d.Verifier))
	authed.Use(middleware.WithRequestTx(db))

	org := authed.Group("/orgs/:org_slug")
	org.Use(middleware.ResolveOrg(d.Orgs, d.Memberships, d.Profiles))

	// Settings (owner).
	org.GET("/scorm/settings", middleware.RequireRole(auth.RoleOwner), handlers.GetScormSettings(d))
	org.PATCH("/scorm/settings", middleware.RequireRole(auth.RoleOwner), handlers.UpdateScormSettings(d))

	// Package authoring + reporting (owner/teacher). The manifest is validated
	// server-side on create; a bad upload is a 400 with the specific reason.
	org.POST("/scorm/packages", authoring, handlers.CreateScormPackage(d))
	org.GET("/scorm/packages", authoring, handlers.ListScormPackages(d))
	org.GET("/scorm/packages/:packageId", authoring, handlers.GetScormPackage(d))
	org.PATCH("/scorm/packages/:packageId", authoring, handlers.UpdateScormPackage(d))
	org.POST("/scorm/packages/:packageId/publish", authoring, handlers.SetScormPackagePublished(d))
	org.DELETE("/scorm/packages/:packageId", authoring, handlers.DeleteScormPackage(d))
	org.GET("/scorm/packages/:packageId/report", authoring, handlers.ScormPackageReport(d))

	// Learner-facing catalog + runtime (any member). launch/commit/finish are
	// distinct path segments so they don't collide with the authoring GET on
	// /scorm/packages/:packageId.
	org.GET("/scorm/catalog", handlers.ListScormCatalog(d))
	org.POST("/scorm/packages/:packageId/launch", handlers.LaunchScormPackage(d))
	org.POST("/scorm/attempts/:attemptId/commit", handlers.CommitScormRuntime(d))
	org.POST("/scorm/attempts/:attemptId/finish", handlers.FinishScormAttempt(d))
	org.GET("/scorm/attempts", handlers.ListMyScormAttempts(d))
}

// registerSimulationsRoutes mounts Task 9's interactive simulations & diagrams
// routes — all authed and org-scoped, reusing the same ResolveOrg/RequireRole
// pattern. Authoring (create/validate a spec, edit, publish, delete) and
// per-simulation reporting are owner/teacher gated; browsing the published
// catalog, recording interaction progress, and viewing one's own progress are
// open to any authenticated org member (RLS scopes progress to the caller);
// settings are owner-only. Every handler independently enforces the two-flag
// feature gate (platform LMS_SIMULATIONS_ENABLED AND the org's
// simulations_enabled) via simGate — the route gating here is authz only. There
// is no public/anonymous surface: a learner always interacts inside an
// authenticated session.
func registerSimulationsRoutes(engine *gin.Engine, d *handlers.AuthDeps, db *pgxpool.Pool) {
	authoring := middleware.RequireRole(auth.RoleOwner, auth.RoleTeacher)

	authed := engine.Group("/api")
	authed.Use(middleware.Authenticate(d.Verifier))
	authed.Use(middleware.WithRequestTx(db))

	org := authed.Group("/orgs/:org_slug")
	org.Use(middleware.ResolveOrg(d.Orgs, d.Memberships, d.Profiles))

	// Settings (owner).
	org.GET("/simulations/settings", middleware.RequireRole(auth.RoleOwner), handlers.GetSimulationSettings(d))
	org.PATCH("/simulations/settings", middleware.RequireRole(auth.RoleOwner), handlers.UpdateSimulationSettings(d))

	// Learner-facing collection routes (any member). Registered as distinct
	// static segments so they don't collide with the :simulationId param below.
	org.GET("/simulations/catalog", handlers.ListSimulationCatalog(d))
	org.GET("/simulations/progress", handlers.ListMySimulationProgress(d))

	// Authoring collection + item (owner/teacher). The spec/config are validated
	// server-side on create/update; a bad payload is a 400 with the reason.
	org.POST("/simulations", authoring, handlers.CreateSimulation(d))
	org.GET("/simulations", authoring, handlers.ListSimulations(d))
	org.GET("/simulations/:simulationId", authoring, handlers.GetSimulation(d))
	org.PATCH("/simulations/:simulationId", authoring, handlers.UpdateSimulation(d))
	org.POST("/simulations/:simulationId/publish", authoring, handlers.SetSimulationPublished(d))
	org.DELETE("/simulations/:simulationId", authoring, handlers.DeleteSimulation(d))
	org.GET("/simulations/:simulationId/report", authoring, handlers.SimulationReport(d))

	// Per-simulation learner runtime (any member): record an interaction and
	// read one's own progress.
	org.POST("/simulations/:simulationId/progress", handlers.RecordSimulationProgress(d))
	org.GET("/simulations/:simulationId/progress", handlers.GetMySimulationProgress(d))
}

// registerPublicSiteRoutes mounts Task 8's PUBLIC, unauthenticated org
// site surfaces: the landing-page builder's rendered output, SEO
// (sitemap/robots), and the embeddable course catalog. No Authenticate/
// WithRequestTx/ResolveOrg at all — these run as anonymous visitors and
// resolve everything through SECURITY DEFINER SQL functions (see
// public_site.go's package doc comment), the same public-route pattern
// certificate verification and unsubscribe already use.
func registerPublicSiteRoutes(engine *gin.Engine, d *handlers.AuthDeps) {
	site := engine.Group("/o/:org_slug")
	site.GET("", handlers.PublicOrgHome(d))
	site.GET("/pages/:slug", handlers.PublicOrgPage(d))
	site.GET("/sitemap.xml", handlers.Sitemap(d))
	site.GET("/robots.txt", handlers.Robots(d))

	// Task 9 Podcasts: the public RSS 2.0 feed a podcast app subscribes to.
	// Same anonymous, SECURITY-DEFINER-resolved pattern as the routes above
	// (see handlers.PodcastRSS).
	site.GET("/podcasts/:show_slug/rss.xml", handlers.PodcastRSS(d))

	engine.GET("/embed/o/:org_slug/catalog", handlers.EmbedCatalog(d))
}

// registerCourseRoutes mounts Task 4's course-domain routes. Course-scoped
// endpoints are flat (/api/courses/:courseId/...) and resolve org context
// via ResolveCourseOrg rather than an :org_slug path segment (see
// plans/task-4-implementation/main-plan.md's Q7). categories/collections
// have no course to derive org context from, so they nest under
// /api/orgs/:org_slug/... reusing the existing ResolveOrg group instead —
// a deliberate, noted exception to the flat-path convention.
func registerCourseRoutes(engine *gin.Engine, d *handlers.AuthDeps, db *pgxpool.Pool, redisClient *redis.Client) {
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

	// The Razorpay payment webhook has the same NO auth/RLS middleware
	// treatment as Bunny's above, for the same reason: an external caller
	// with no session, bearer token, or org context. Signature
	// verification (via d.Payments.VerifyWebhookSignature) is this
	// route's only authentication mechanism, by design — see
	// handlers.RazorpayWebhook's doc comment for the full division of
	// responsibility between this handler and the Task 8 worker job.
	//
	// Rate limit: generous (100/min, per client IP) rather than the
	// auth-route 5-per-15-min shape — Razorpay's own legitimate retry
	// behavior during an incident must never be throttled into a dropped
	// event (a dropped webhook means a paying learner never gets their
	// entitlement). ratelimit.Limiter.Allow fails open on a Redis outage
	// (see internal/ratelimit/ratelimit.go), so this is a safety margin
	// on top of the generous limit, not a substitute for it.
	webhookLimit := ratelimit.New(redisClient, "ratelimit:webhook-razorpay", 100, time.Minute)
	engine.POST("/api/webhooks/razorpay", middleware.RateLimit(webhookLimit, middleware.ByClientIP), handlers.RazorpayWebhook(d))

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

// registerCommerceRoutes mounts Task 6's commerce routes: owner/teacher
// offer, discount-code, invite-token, and manual-grant management
// (course-scoped, reusing registerCourseRoutes' /api/courses/:courseId +
// ResolveCourseOrg convention); the learner-facing checkout page and
// order-creation endpoint (also course-scoped, since every handler's own
// doc comment — see commerce_checkout.go — nests them under
// /api/courses/:courseId/offers/:offerId/checkout, not the flat
// /checkout/:offerId this task's own doc speculated before the handlers
// actually landed); the standalone order-status JSON poll endpoint (no
// course/org in its path, per commerce_checkout.go's OrderStatus doc
// comment); and the org-scoped refund/revenue-report endpoints (per
// commerce_refunds.go's RefundOrder and commerce_reports.go's
// RevenueReport doc comments, both /api/orgs/:org_slug/..., NOT
// course-scoped as this task's own doc spectulated). It also mounts this
// task's own new order-status "processing" HTML page and its htmx polling
// fragment endpoint — see order_status_ui.go.
//
// Every RequireRole call below is cross-checked against
// internal/auth/permissions.go's commerceDomainActions (owner+teacher:
// offer/discount/invite-token/entitlement-grant management) and
// ownerOnlyCommerceDomainActions (owner-only: refund.initiate,
// report.revenue.view). Checkout/order-creation/order-status are
// deliberately NOT RequireRole-gated — any authenticated org member (or,
// for order-status, the purchasing learner themself) may reach them, per
// CheckoutPage/CreateOrder/OrderStatus's own doc comments.
func registerCommerceRoutes(engine *gin.Engine, d *handlers.AuthDeps, db *pgxpool.Pool, redisClient *redis.Client) {
	authoring := middleware.RequireRole(auth.RoleOwner, auth.RoleTeacher)

	authed := engine.Group("/api")
	authed.Use(middleware.Authenticate(d.Verifier))
	authed.Use(middleware.WithRequestTx(db))

	// Checkout create-order is abuse-prone the same way login/register
	// are (probing pricing/discount logic, exhausting Razorpay API
	// quota). Keyed by user ID rather than ByClientIP: this route is
	// always authenticated (ResolveCourseOrg requires an org member), and
	// IP-based limiting risks false positives for legitimate buyers
	// sharing an IP (office/campus NAT, family) more than the auth
	// routes' pre-authentication login-abuse case does.
	checkoutLimit := ratelimit.New(redisClient, "ratelimit:checkout-create", 5, 15*time.Minute)
	checkoutLimitByUser := func(c *gin.Context) string {
		if ac, ok := middleware.AuthContextFromGin(c); ok {
			return ac.UserID
		}
		return middleware.ByClientIP(c)
	}

	// Course-scoped commerce management + checkout, reusing the exact
	// ResolveCourseOrg convention registerCourseRoutes/registerLearnerRoutes
	// already use — a separate group local to this function, not shared
	// across functions, matching this codebase's existing style.
	course := authed.Group("/courses/:courseId")
	course.Use(middleware.ResolveCourseOrg(d.Courses, d.Memberships, d.Profiles))

	course.POST("/offers", authoring, handlers.CreateOffer(d))
	course.GET("/offers", authoring, handlers.ListOffers(d))
	course.PATCH("/offers/:offerId", authoring, handlers.UpdateOffer(d))
	course.POST("/offers/:offerId/archive", authoring, handlers.ArchiveOffer(d))

	course.POST("/offers/:offerId/discounts", authoring, handlers.CreateDiscountCode(d))
	course.GET("/offers/:offerId/discounts", authoring, handlers.ListDiscountCodes(d))
	course.POST("/offers/:offerId/discounts/:discountId/deactivate", authoring, handlers.DeactivateDiscountCode(d))

	course.POST("/offers/:offerId/invite-tokens", authoring, handlers.CreateInviteToken(d))
	course.GET("/offers/:offerId/invite-tokens", authoring, handlers.ListInviteTokens(d))

	// Manual, non-payment entitlement grant — owner/teacher, reason
	// required by the handler itself (see commerce_refunds.go.GrantAccess).
	course.POST("/grant-access", authoring, handlers.GrantAccess(d))

	// Checkout page + order creation: ResolveCourseOrg only, no RequireRole
	// — any org member (not staff-gated) may reach these, matching
	// CheckoutPage/CreateOrder's own doc comments. Only the POST (order
	// creation) is rate limited.
	course.GET("/offers/:offerId/checkout", handlers.CheckoutPage(d))
	course.POST("/offers/:offerId/checkout/order",
		middleware.RateLimit(checkoutLimit, checkoutLimitByUser),
		handlers.CreateOrder(d))

	// Order-status JSON poll: no :courseId/:org_slug in its path at all —
	// see OrderStatus's doc comment for why it resolves the caller's own
	// order via orders.learner_id = app_current_user_id() rather than any
	// org-resolving middleware.
	authed.GET("/orders/:orderId/status", handlers.OrderStatus(d))

	// Org-scoped, owner-only: refunds and revenue visibility. Reuses the
	// ResolveOrg convention registerOrgRoutes already establishes — a
	// separate group local to this function.
	org := authed.Group("/orgs/:org_slug")
	org.Use(middleware.ResolveOrg(d.Orgs, d.Memberships, d.Profiles))
	org.POST("/orders/:orderId/refund", middleware.RequireRole(auth.RoleOwner), handlers.RefundOrder(d))
	org.GET("/reports/revenue", middleware.RequireRole(auth.RoleOwner), handlers.RevenueReport(d))

	// This task's own new UI glue: the order-status "processing" page a
	// learner's browser lands on after Razorpay's checkout.js handler
	// navigates them here (see templates/checkout.html), and the htmx
	// polling fragment it hits every 2 seconds. Cookie-authenticated HTML,
	// GET-only, no CSRF — matching registerLearnerUIRoutes' precedent
	// (nothing here POSTs/PATCHes/DELETEs directly). Mounted at the flat
	// /orders/:orderId/... prefix (no /api) since templates/checkout.html
	// already navigates to exactly this path on Razorpay's success
	// callback — see order_status_ui.go's doc comments for the full
	// design rationale.
	pages := engine.Group("")
	pages.Use(middleware.Authenticate(d.Verifier))
	pages.Use(middleware.WithRequestTx(db))
	pages.GET("/orders/:orderId/status", handlers.OrderStatusPage(d))
	pages.GET("/orders/:orderId/status-fragment", handlers.OrderStatusFragment(d))
}

// registerAdminUIRoutes mounts Task 9's read-only admin dashboard pages:
// the org-scoped dashboard (reusing the ResolveOrg middleware chain, same
// construction as registerOrgRoutes' JSON group above, mounted at the
// top level rather than under /api since this is an HTML page —
// following registerLearnerUIRoutes' precedent for HTML routes) and the
// two platform-wide pages, registered outside any ResolveOrg group per
// this task's explicit routing note (RequirePlatformOwner has no org
// context to resolve, see middleware/org.go's doc comment). No CSRF
// middleware here: every route in this file is GET-only and mutates
// nothing (see admin_ui.go's package doc comment).
func registerAdminUIRoutes(engine *gin.Engine, d *handlers.AuthDeps, db *pgxpool.Pool) {
	authed := engine.Group("")
	authed.Use(middleware.Authenticate(d.Verifier))
	authed.Use(middleware.WithRequestTx(db))

	org := authed.Group("/orgs/:org_slug")
	org.Use(middleware.ResolveOrg(d.Orgs, d.Memberships, d.Profiles))
	org.GET("/admin", middleware.RequireRole(auth.RoleOwner), handlers.OrgAdminDashboardPage(d))

	// Platform-wide: no :org_slug, no ResolveOrg — RequirePlatformOwner is
	// the only gate, and no permissionMatrix/Can() check is involved at
	// all, per the commerce spec and task-3-permissions-matrix.md's note
	// that this cross-org view is not org-scoped.
	platformOwnerOnly := middleware.RequirePlatformOwner(d.Profiles)
	authed.GET("/admin/organizations", platformOwnerOnly, handlers.PlatformAdminDashboardPage(d))
	authed.GET("/admin/organizations/:org_slug", platformOwnerOnly, handlers.PlatformAdminOrgDetailPage(d))
}

// registerCommunityRoutes mounts Task 7's discussions, moderation,
// notifications, preferences, and collaborative-board JSON API. Everything
// requires authentication and a request-scoped transaction so RLS session
// variables are in effect. Org-scoped surfaces resolve via ResolveOrg or
// ResolveCourseOrg; thread/post/board action routes are keyed only by a row
// id, so they resolve org context from the row itself via the Task 7
// Resolve{Thread,Post,Board}Org middleware. Moderator/owner-only actions are
// additionally gated by RequireRole (defense-in-depth over is_org_moderator
// RLS). Notification routes are recipient-scoped (RLS by recipient_id), so
// they carry no org segment.
func registerCommunityRoutes(engine *gin.Engine, d *handlers.AuthDeps, db *pgxpool.Pool) {
	moderatorOrOwner := middleware.RequireRole(auth.RoleModerator, auth.RoleOwner)

	authed := engine.Group("/api")
	authed.Use(middleware.Authenticate(d.Verifier))
	authed.Use(middleware.WithRequestTx(db))

	// Org-scoped: org-wide threads, broadcasts, moderation queue, preferences.
	org := authed.Group("/orgs/:org_slug")
	org.Use(middleware.ResolveOrg(d.Orgs, d.Memberships, d.Profiles))
	org.POST("/threads", handlers.CreateOrgThread(d))
	org.GET("/threads", handlers.ListOrgThreads(d))
	org.POST("/broadcasts", middleware.RequireRole(auth.RoleOwner, auth.RoleTeacher), handlers.CreateBroadcast(d))
	org.GET("/reports", moderatorOrOwner, handlers.ListReports(d))
	org.POST("/reports/:reportId/resolve", moderatorOrOwner, handlers.ResolveReport(d))
	org.POST("/reports/:reportId/dismiss", moderatorOrOwner, handlers.DismissReport(d))
	org.GET("/notification-preferences", handlers.GetNotificationPreferences(d))
	org.PATCH("/notification-preferences", handlers.UpdateNotificationPreference(d))

	// Task 9 "improved collaborative boards": org-level board templates. Any
	// member may read/seed from a template; teachers/owners author them.
	teacherOrOwner := middleware.RequireRole(auth.RoleOwner, auth.RoleTeacher)
	org.POST("/board-templates", teacherOrOwner, handlers.CreateBoardTemplate(d))
	org.GET("/board-templates", handlers.ListBoardTemplates(d))
	org.GET("/board-templates/:templateId", handlers.GetBoardTemplate(d))
	org.PATCH("/board-templates/:templateId", teacherOrOwner, handlers.UpdateBoardTemplate(d))
	org.DELETE("/board-templates/:templateId", teacherOrOwner, handlers.DeleteBoardTemplate(d))

	// Course-scoped: course discussion threads + collaborative boards.
	course := authed.Group("/courses/:courseId")
	course.Use(middleware.ResolveCourseOrg(d.Courses, d.Memberships, d.Profiles))
	course.POST("/threads", handlers.CreateCourseThread(d))
	course.GET("/threads", handlers.ListCourseThreads(d))
	course.POST("/boards", handlers.CreateBoard(d))
	course.GET("/boards", handlers.ListBoards(d))

	// Thread-scoped (org resolved from the thread row).
	thread := authed.Group("/threads/:threadId")
	thread.Use(middleware.ResolveThreadOrg(d.Threads, d.Memberships, d.Profiles))
	thread.GET("", handlers.GetThread(d))
	thread.GET("/members", handlers.ThreadMembers(d))
	thread.POST("/posts", handlers.CreatePost(d))
	thread.POST("/pin", moderatorOrOwner, handlers.SetThreadPinned(d))
	thread.POST("/lock", moderatorOrOwner, handlers.SetThreadLocked(d))

	// Post-scoped (org resolved from the post row). Edit/delete are author-or-
	// moderator (checked in-handler + RLS); hide is moderator/owner only.
	post := authed.Group("/posts/:postId")
	post.Use(middleware.ResolvePostOrg(d.Posts, d.Memberships, d.Profiles))
	post.PATCH("", handlers.EditPost(d))
	post.DELETE("", handlers.DeletePost(d))
	post.POST("/reactions", handlers.ReactToPost(d))
	post.DELETE("/reactions", handlers.Unreact(d))
	post.POST("/report", handlers.ReportPost(d))
	post.POST("/hide", moderatorOrOwner, handlers.HidePost(d))

	// Board-scoped (org resolved from the board row).
	board := authed.Group("/boards/:boardId")
	board.Use(middleware.ResolveBoardOrg(d.Boards, d.Memberships, d.Profiles))
	board.GET("", handlers.GetBoard(d))
	board.DELETE("", handlers.DeleteBoard(d))
	// Task 9 "improved collaborative boards": versioned checkpoints. Any member
	// may save/restore a checkpoint; a checkpoint is pruned by its author or a
	// moderator/owner (enforced in-handler + RLS).
	board.POST("/versions", handlers.SaveBoardVersion(d))
	board.GET("/versions", handlers.ListBoardVersions(d))
	board.POST("/versions/:versionId/restore", handlers.RestoreBoardVersion(d))
	board.DELETE("/versions/:versionId", handlers.DeleteBoardVersion(d))

	// Recipient-scoped notifications. Paths avoid a param/static segment clash
	// at the same tree position (gin would panic): list, read-all, and
	// mark-read/:id all use a distinct static second segment.
	authed.GET("/notifications", handlers.ListNotifications(d))
	authed.POST("/notifications/read-all", handlers.MarkAllNotificationsRead(d))
	authed.POST("/notifications/mark-read/:id", handlers.MarkNotificationRead(d))

	// Nav unread badge (HTML fragment, cookie auth) — top-level so HTMX in
	// server-rendered pages can poll it.
	navAuthed := engine.Group("")
	navAuthed.Use(middleware.Authenticate(d.Verifier))
	navAuthed.Use(middleware.WithRequestTx(db))
	navAuthed.GET("/notifications/unread-count", handlers.NotificationsUnreadCount(d))
}

// registerRealtimeRoutes mounts the Task 7 WebSocket endpoints. They
// authenticate via the session cookie (Authenticate) and authorize with a
// direct membership check inside the handler — deliberately NOT WithRequestTx,
// since a long-lived socket must not hold a request transaction open.
// registerCommunityUIRoutes mounts Task 7's server-rendered HTML pages
// (community, thread, notifications, board, moderation queue). Cookie auth +
// WithRequestTx like the other UI surfaces; each thin page drives the JSON API
// with same-origin fetch.
func registerCommunityUIRoutes(engine *gin.Engine, d *handlers.AuthDeps, db *pgxpool.Pool) {
	authed := engine.Group("")
	authed.Use(middleware.Authenticate(d.Verifier))
	authed.Use(middleware.WithRequestTx(db))

	authed.GET("/notifications", handlers.NotificationsPage(d))

	org := authed.Group("/orgs/:org_slug")
	org.Use(middleware.ResolveOrg(d.Orgs, d.Memberships, d.Profiles))
	org.GET("/community", handlers.CommunityPage(d))
	org.GET("/moderation", middleware.RequireRole(auth.RoleModerator, auth.RoleOwner), handlers.ModerationPage(d))

	thread := authed.Group("/community/threads/:threadId")
	thread.Use(middleware.ResolveThreadOrg(d.Threads, d.Memberships, d.Profiles))
	thread.GET("", handlers.ThreadPage(d))

	board := authed.Group("/community/boards/:boardId")
	board.Use(middleware.ResolveBoardOrg(d.Boards, d.Memberships, d.Profiles))
	board.GET("", handlers.BoardPage(d))
}

func registerRealtimeRoutes(engine *gin.Engine, d *handlers.AuthDeps, hub *realtime.Hub) {
	ws := engine.Group("/ws")
	ws.Use(middleware.Authenticate(d.Verifier))
	ws.GET("/courses/:courseId/presence", handlers.CoursePresenceSocket(d, hub))
	ws.GET("/boards/:boardId", handlers.BoardSocket(d, hub))
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
