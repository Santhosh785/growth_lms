package handlers

import (
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"growth-lms/internal/auth"
	"growth-lms/internal/config"
	"growth-lms/internal/media"
	"growth-lms/internal/models"
)

// AuthDeps bundles everything the auth/org/membership/invitation/api-token
// AND course-domain handlers need. Built once at startup and closed over
// by each handler's constructor function, so handlers stay plain
// gin.HandlerFunc values.
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

	// Task 4: course domain.
	Courses            *models.CourseRepo
	Chapters           *models.ChapterRepo
	Lessons            *models.LessonRepo
	Blocks             *models.BlockRepo
	Assets             *models.AssetRepo
	Categories         *models.CategoryRepo
	Tags               *models.TagRepo
	Collections        *models.CollectionRepo
	CourseVersions     *models.CourseVersionRepo
	CoursePrereqs      *models.CoursePrerequisiteRepo
	CompletionRules    *models.CourseCompletionRuleRepo
	Bunny              media.BunnyClient
	Storage            media.StorageClient
	AsyncQueue         *asynq.Client
}
