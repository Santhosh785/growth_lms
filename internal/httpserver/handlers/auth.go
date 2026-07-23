package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"growth-lms/internal/auth"
	"growth-lms/internal/config"
	"growth-lms/internal/httpserver/middleware"
	"growth-lms/internal/models"
)

const refreshCookieName = "lms_refresh"

func setSessionCookies(c *gin.Context, cfg *config.Config, session *auth.Session) {
	secure := cfg.Env != config.EnvDevelopment
	maxAge := session.ExpiresIn
	if maxAge <= 0 {
		maxAge = 3600
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(middleware.SessionCookieName, session.AccessToken, maxAge, "/", "", secure, true)
	// Refresh token lives longer than the access token; the silent-refresh
	// flow that would consume it is out of scope for this task, but the
	// cookie is set now so it doesn't need a second auth-flow change later.
	c.SetCookie(refreshCookieName, session.RefreshToken, 30*24*3600, "/", "", secure, true)
}

func clearSessionCookies(c *gin.Context, cfg *config.Config) {
	secure := cfg.Env != config.EnvDevelopment
	c.SetCookie(middleware.SessionCookieName, "", -1, "/", "", secure, true)
	c.SetCookie(refreshCookieName, "", -1, "/", "", secure, true)
}

type registerRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
}

// Register proxies signup to Supabase Auth. The Go backend never stores
// or hashes the password itself. A profiles row is created automatically
// by the on_auth_user_created trigger once Supabase creates the
// auth.users row, so nothing further needs to happen here.
func Register(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req registerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		if err := d.Supabase.SignUp(c.Request.Context(), req.Email, req.Password); err != nil {
			_ = d.Audit.Record(c.Request.Context(), d.Pool, models.AuditEvent{
				Action: "auth.register_failed", ResourceType: "profile",
				Details:   map[string]any{"email": req.Email},
				IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
			})
			c.JSON(http.StatusBadRequest, gin.H{"error": "registration failed"})
			return
		}

		_ = d.Audit.Record(c.Request.Context(), d.Pool, models.AuditEvent{
			Action: "auth.register", ResourceType: "profile",
			Details:   map[string]any{"email": req.Email},
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusCreated, gin.H{"message": "check your email to verify your account"})
	}
}

// AdminRegister creates a verified user via the Admin API, bypassing email
// confirmation. This is a privileged provisioning action for admin/test user
// creation, not public signup — its route is gated behind platform-owner auth
// (middleware.RequirePlatformOwner) and a per-IP rate limit in server.go.
func AdminRegister(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req registerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		if err := d.Supabase.AdminCreateUser(c.Request.Context(), req.Email, req.Password, true); err != nil {
			_ = d.Audit.Record(c.Request.Context(), d.Pool, models.AuditEvent{
				Action: "auth.admin_register_failed", ResourceType: "profile",
				Details:   map[string]any{"email": req.Email},
				IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
			})
			c.JSON(http.StatusBadRequest, gin.H{"error": "admin registration failed"})
			return
		}

		_ = d.Audit.Record(c.Request.Context(), d.Pool, models.AuditEvent{
			Action: "auth.admin_register", ResourceType: "profile",
			Details:   map[string]any{"email": req.Email},
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusCreated, gin.H{"message": "user created successfully"})
	}
}

type verifyEmailRequest struct {
	TokenHash string `json:"token_hash" binding:"required"`
}

// VerifyEmail exchanges the token from the confirmation email for a
// session, logging the caller in immediately on success.
func VerifyEmail(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req verifyEmailRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		session, err := d.Supabase.VerifyOTP(c.Request.Context(), req.TokenHash, "signup")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or expired verification link"})
			return
		}

		setSessionCookies(c, d.Config, session)
		userID := session.User.ID
		_ = d.Audit.Record(c.Request.Context(), d.Pool, models.AuditEvent{
			UserID: &userID, Action: "auth.email_verified", ResourceType: "profile", ResourceID: &userID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusOK, gin.H{"user": gin.H{"id": session.User.ID, "email": session.User.Email}})
	}
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// loginBackoffLimit and loginBackoffWindow implement the spec's
// "exponential backoff on failed logins to the same email": each failed
// attempt doubles the lockout window (capped), tracked in Redis
// independent of the generic per-IP rate limiter already applied to this
// route.
const (
	loginBackoffMaxWindow = 30 * time.Minute
	loginBackoffBase      = 30 * time.Second
	loginBackoffThreshold = 3 // attempts before backoff kicks in
)

// Login proxies password sign-in to Supabase, setting a Go-managed
// session cookie on success. Applies a per-email exponential backoff on
// top of the per-IP rate limiter wired in server.go, so a distributed
// attacker guessing one account's password from many IPs is still
// slowed down.
func Login(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req loginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		email := strings.ToLower(strings.TrimSpace(req.Email))

		if locked, retryAfter := checkLoginBackoff(c.Request.Context(), d.Redis, email); locked {
			c.Header("Retry-After", retryAfter.String())
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many failed attempts, try again later"})
			return
		}

		session, err := d.Supabase.SignInWithPassword(c.Request.Context(), email, req.Password)
		if err != nil {
			failCount := recordLoginFailure(c.Request.Context(), d.Redis, email)
			_ = d.Audit.Record(c.Request.Context(), d.Pool, models.AuditEvent{
				Action: "auth.login_failed", ResourceType: "profile",
				Details:   map[string]any{"email": email},
				IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
			})
			// One-shot auth alert exactly when the failure threshold is first
			// crossed (repeated failures for one account = possible brute force
			// or credential-stuffing). checkLoginBackoff blocks further
			// attempts while locked, so this fires once per burst, not per try.
			if failCount == loginBackoffThreshold {
				d.recordAlert(c.Request.Context(), models.AlertSeverityWarning, models.AlertCategoryAuth,
					"login_backoff", "repeated failed logins for a single account (possible brute force)",
					map[string]any{"email": email, "failures": failCount, "client_ip": c.ClientIP()})
			}
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid email or password"})
			return
		}

		// Refuse a session to a suspended account. Checked via a SECURITY
		// DEFINER helper on the raw pool (no RLS session context exists yet).
		// A failed lookup is treated as not-suspended so a transient DB error
		// never locks out every user, but the failed attempt is not cleared.
		if suspended, serr := d.Profiles.IsSuspended(c.Request.Context(), d.Pool, session.User.ID); serr == nil && suspended {
			uid := session.User.ID
			_ = d.Audit.Record(c.Request.Context(), d.Pool, models.AuditEvent{
				UserID: &uid, Action: "auth.login_suspended", ResourceType: "profile", ResourceID: &uid,
				IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
			})
			c.JSON(http.StatusForbidden, gin.H{"error": "this account has been suspended"})
			return
		}

		clearLoginFailures(c.Request.Context(), d.Redis, email)
		setSessionCookies(c, d.Config, session)
		userID := session.User.ID
		_ = d.Audit.Record(c.Request.Context(), d.Pool, models.AuditEvent{
			UserID: &userID, Action: "auth.login", ResourceType: "profile", ResourceID: &userID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusOK, gin.H{"user": gin.H{"id": session.User.ID, "email": session.User.Email}})
	}
}

type passwordResetRequestRequest struct {
	Email string `json:"email" binding:"required,email"`
}

// PasswordResetRequest always responds 200 regardless of whether the
// email is registered, so the endpoint cannot be used to enumerate
// accounts.
func PasswordResetRequest(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req passwordResetRequestRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		_ = d.Supabase.RequestPasswordReset(c.Request.Context(), req.Email)
		_ = d.Audit.Record(c.Request.Context(), d.Pool, models.AuditEvent{
			Action: "auth.password_reset_requested", ResourceType: "profile",
			Details:   map[string]any{"email": req.Email},
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusOK, gin.H{"message": "if that email is registered, a reset link has been sent"})
	}
}

type passwordResetRequest struct {
	AccessToken string `json:"access_token" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

// PasswordReset completes a reset using the access token Supabase issued
// via the recovery link (captured client-side from the redirect and
// posted here — see the HTML page for how the token reaches this call).
func PasswordReset(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req passwordResetRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}

		if err := d.Supabase.UpdatePassword(c.Request.Context(), req.AccessToken, req.NewPassword); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unable to reset password"})
			return
		}

		_ = d.Audit.Record(c.Request.Context(), d.Pool, models.AuditEvent{
			Action: "auth.password_reset", ResourceType: "profile",
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusOK, gin.H{"message": "password updated"})
	}
}

// Logout signs the current session out of Supabase and clears the
// session cookies. Requires Authenticate to have run.
func Logout(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, _ := middleware.AuthContextFromGin(c)

		if token, err := c.Cookie(middleware.SessionCookieName); err == nil && token != "" {
			_ = d.Supabase.SignOut(c.Request.Context(), token)
		}
		clearSessionCookies(c, d.Config)

		_ = d.Audit.Record(c.Request.Context(), d.Pool, models.AuditEvent{
			UserID: &ac.UserID, Action: "auth.logout", ResourceType: "profile", ResourceID: &ac.UserID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})
		c.JSON(http.StatusOK, gin.H{"message": "logged out"})
	}
}

// DeleteAccount permanently deletes the caller's Supabase Auth user,
// which cascades (via FK ON DELETE CASCADE) to their profiles row and
// every membership. Requires Authenticate to have run.
func DeleteAccount(d *AuthDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		ac, _ := middleware.AuthContextFromGin(c)

		_ = d.Audit.Record(c.Request.Context(), d.Pool, models.AuditEvent{
			UserID: &ac.UserID, Action: "auth.account_deletion_requested", ResourceType: "profile", ResourceID: &ac.UserID,
			IPAddress: c.ClientIP(), UserAgent: c.Request.UserAgent(),
		})

		if err := d.Supabase.AdminDeleteUser(c.Request.Context(), ac.UserID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to delete account"})
			return
		}

		clearSessionCookies(c, d.Config)
		c.JSON(http.StatusOK, gin.H{"message": "account deleted"})
	}
}

func checkLoginBackoff(ctx context.Context, r *redis.Client, email string) (locked bool, retryAfter time.Duration) {
	count, err := r.Get(ctx, loginFailKey(email)).Int()
	if err != nil || count < loginBackoffThreshold {
		return false, 0
	}
	ttl, err := r.TTL(ctx, loginFailKey(email)).Result()
	if err != nil || ttl <= 0 {
		return false, 0
	}
	return true, ttl
}

// recordLoginFailure increments the per-email failure counter and returns the
// new count (0 on a Redis error). The caller uses the returned count to raise a
// one-shot auth alert exactly when the threshold is first crossed.
func recordLoginFailure(ctx context.Context, r *redis.Client, email string) int64 {
	key := loginFailKey(email)
	count, err := r.Incr(ctx, key).Result()
	if err != nil {
		return 0
	}
	if count < loginBackoffThreshold {
		return count
	}
	window := loginBackoffBase * time.Duration(1<<uint(count-loginBackoffThreshold))
	if window > loginBackoffMaxWindow {
		window = loginBackoffMaxWindow
	}
	_ = r.Expire(ctx, key, window).Err()
	return count
}

func clearLoginFailures(ctx context.Context, r *redis.Client, email string) {
	_ = r.Del(ctx, loginFailKey(email)).Err()
}

func loginFailKey(email string) string {
	return "auth:login_fail:" + email
}
