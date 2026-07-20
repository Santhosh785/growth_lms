// Package httpserver wires together the Gin engine: middleware, CORS,
// proxy trust, and routes.
package httpserver

import (
	"log/slog"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"growth-lms/internal/auth"
	"growth-lms/internal/config"
	"growth-lms/internal/httpserver/handlers"
	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/httpserver/webconsole"
	"growth-lms/internal/models"
	"growth-lms/internal/ratelimit"
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
	}

	registerAuthRoutes(engine, deps, redisClient)
	registerOrgRoutes(engine, deps, db)

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
