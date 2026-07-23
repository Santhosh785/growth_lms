package handlers

import (
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"growth-lms/internal/ai"
	"growth-lms/internal/auth"
	"growth-lms/internal/config"
	"growth-lms/internal/media"
	"growth-lms/internal/models"
	"growth-lms/internal/payments"
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
	Courses         *models.CourseRepo
	Chapters        *models.ChapterRepo
	Lessons         *models.LessonRepo
	Blocks          *models.BlockRepo
	Assets          *models.AssetRepo
	Categories      *models.CategoryRepo
	Tags            *models.TagRepo
	Collections     *models.CollectionRepo
	CourseVersions  *models.CourseVersionRepo
	CoursePrereqs   *models.CoursePrerequisiteRepo
	CompletionRules *models.CourseCompletionRuleRepo
	Bunny           media.BunnyClient
	Storage         media.StorageClient
	AsyncQueue      *asynq.Client

	// Task 5: learner journey.
	LearnerCourseAccess   *models.LearnerCourseAccessRepo
	ResumePositions       *models.LearnerResumePositionRepo
	LearnerProgress       *models.LearnerLessonProgressRepo
	Certificates          *models.LearnerCertificateRepo
	QuizAttempts          *models.LearnerQuizAttemptRepo
	QuizScores            *models.LearnerQuizScoreRepo
	AssignmentSubmissions *models.LearnerAssignmentSubmissionRepo
	AssignmentGrades      *models.LearnerAssignmentGradeRepo
	Announcements         *models.CourseAnnouncementRepo

	// Task 6: commerce.
	Payments          payments.Provider
	WebhookEvents     *models.WebhookEventRepo
	Offers            *models.OfferRepo
	DiscountCodes     *models.DiscountCodeRepo
	InviteTokens      *models.InviteTokenRepo
	Orders            *models.OrderRepo
	Entitlements      *models.EntitlementRepo
	CommercePayments  *models.PaymentRepo
	Refunds           *models.RefundRepo
	PaymentAuditTrail *models.PaymentAuditRepo
	PlatformSettings  *models.PlatformSettingsRepo

	// Task 7: communities, notifications, collaboration.
	Threads           *models.DiscussionThreadRepo
	Posts             *models.DiscussionPostRepo
	Reactions         *models.PostReactionRepo
	Mentions          *models.PostMentionRepo
	Reports           *models.ContentReportRepo
	Notifications     *models.NotificationRepo
	NotificationPrefs *models.NotificationPreferenceRepo
	UnsubscribeTokens *models.UnsubscribeTokenRepo
	Boards            *models.CollabBoardRepo

	// Task 8: analytics, search, SEO, themes, and public pages.
	AnalyticsEvents  *models.AnalyticsEventRepo
	AnalyticsRollups *models.AnalyticsRollupRepo
	OrgPages         *models.OrgPageRepo
	Search           *models.SearchRepo

	// Task 9: AI authoring & tutors.
	AI            *ai.Service
	AIGenerations *models.AIGenerationRepo
	AIUsage       *models.AIUsageRepo
	AITutor       *models.AITutorRepo

	// Task 9: podcasts & RSS.
	PodcastShows     *models.PodcastShowRepo
	PodcastEpisodes  *models.PodcastEpisodeRepo
	PodcastPlaylists *models.PodcastPlaylistRepo
	PodcastProgress  *models.PodcastProgressRepo
}
