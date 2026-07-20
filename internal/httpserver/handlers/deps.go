package handlers

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"growth-lms/internal/auth"
	"growth-lms/internal/config"
	"growth-lms/internal/models"
)

// AuthDeps bundles everything the auth/org/membership/invitation/api-token
// handlers need. Built once at startup and closed over by each handler's
// constructor function, so handlers stay plain gin.HandlerFunc values.
type AuthDeps struct {
	Config      *config.Config
	Pool        *pgxpool.Pool
	Redis       *redis.Client
	Verifier    *auth.Verifier
	Supabase    auth.Client
	Profiles    *models.ProfileRepo
	Orgs        *models.OrgRepo
	Memberships *models.MembershipRepo
	Invitations *models.InvitationRepo
	Audit       *models.AuditRepo
	APITokens   *models.APITokenRepo
}
