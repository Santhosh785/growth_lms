// Package worker runs the Redis-backed background job consumer (asynq).
// It shares the same binary, config, and dependency wiring as the API
// server; only the entrypoint command differs.
package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/hibiken/asynq"

	"growth-lms/internal/config"
	"growth-lms/internal/db"
	"growth-lms/internal/models"
	"growth-lms/internal/notify"
)

// Queue names. Task 5/6 register real task handlers (email sends, webhook
// processing) against this mux as those domains land.
const (
	QueueDefault  = "default"
	QueueCritical = "critical"
)

// publishSweepInterval is how often the scheduled-publish sweep runs (see
// publish.go). One minute matches the spec's "scheduled asynq job"
// description closely enough for an MVP without needing per-course
// delayed-task bookkeeping.
const publishSweepInterval = time.Minute

// abandonOrdersSweepInterval and expireEntitlementsSweepInterval are
// defined in orders.go/entitlements.go respectively (Task 8), following
// this same const-per-sweep precedent.

// Run starts the asynq server and blocks until it shuts down or ctx is
// canceled. redisAddr/opts come from cfg.Redis.URL parsed by the caller.
func Run(cfg *config.Config, redisOpt asynq.RedisConnOpt, logger *slog.Logger) error {
	// Task 4 is the first worker consumer that needs direct DB access:
	// the scheduled-publish sweep and the Bunny webhook task both update
	// Postgres directly, at the pool's own admin privileges — there's no
	// per-request caller to scope RLS session variables to for a
	// background job.
	pool, err := db.NewPool(context.Background(), cfg.Database.URL)
	if err != nil {
		return err
	}
	defer pool.Close()

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: 10,
		Queues: map[string]int{
			QueueCritical: 6,
			QueueDefault:  4,
		},
		Logger: newAsynqLogger(logger),
		// Task 10 observability: record a system_alert when a task fails for
		// good (retries exhausted), so operators see failed jobs in the admin
		// alert stream, not just in logs.
		ErrorHandler: jobFailureAlertHandler(pool, models.NewAlertRepo(), logger),
	})

	profiles := models.NewProfileRepo()
	emailClient := notify.NewResendClient(cfg.Resend)

	// Task 7 community notification handlers share one dependency bundle.
	community := &communityDeps{
		pool:          pool,
		profiles:      profiles,
		memberships:   models.NewMembershipRepo(),
		notifications: models.NewNotificationRepo(),
		prefs:         models.NewNotificationPreferenceRepo(),
		unsub:         models.NewUnsubscribeTokenRepo(),
		email:         emailClient,
		baseURL:       cfg.BaseURL,
	}

	// Task 8's payment.captured handler enqueues a receipt-email task
	// from within a task handler, which needs its own asynq.Client (the
	// server above only consumes) — same producer/consumer split
	// tasks.go's NewClient documents for the HTTP server process.
	asyncClient := NewClient(redisOpt)
	defer asyncClient.Close()

	mux := asynq.NewServeMux()
	mux.HandleFunc(TypeBunnyTranscodeComplete, handleBunnyTranscodeComplete(pool))
	mux.HandleFunc(TypeNotifyAssignmentGraded, handleNotifyAssignmentGraded(pool, profiles, emailClient))
	mux.HandleFunc(TypeNotifyCertificateIssued, handleNotifyCertificateIssued(pool, profiles, emailClient))
	mux.HandleFunc(TypeNotifyCourseAnnouncement, handleNotifyCourseAnnouncement(pool, profiles, emailClient))
	mux.HandleFunc(TypeNotifyCourseReminder, handleNotifyCourseReminder(pool, profiles, emailClient))
	mux.HandleFunc(TypeRazorpayWebhook, handleRazorpayWebhook(pool, asyncClient, logger))
	mux.HandleFunc(TypeSendReceiptEmail, handleSendReceiptEmail(pool, profiles, emailClient))
	mux.HandleFunc(TypeNotifyMention, handleNotifyMention(community))
	mux.HandleFunc(TypeNotifyReply, handleNotifyReply(community))
	mux.HandleFunc(TypeNotifyReportFiled, handleNotifyReportFiled(community))
	mux.HandleFunc(TypeNotifyBroadcast, handleNotifyBroadcast(community))

	sweepCtx, cancelSweep := context.WithCancel(context.Background())
	defer cancelSweep()
	go runPublishSweepLoop(sweepCtx, pool, logger, publishSweepInterval)
	go runAbandonOrdersSweepLoop(sweepCtx, pool, logger, abandonOrdersSweepInterval)
	go runExpireEntitlementsSweepLoop(sweepCtx, pool, logger, expireEntitlementsSweepInterval)
	go runAnalyticsRollupSweepLoop(sweepCtx, pool, logger, analyticsRollupSweepInterval)

	logger.Info("worker starting", "env", cfg.Env)
	return srv.Run(mux)
}

// asynqLogger adapts slog.Logger to asynq's logging interface.
type asynqLogger struct {
	l *slog.Logger
}

func newAsynqLogger(l *slog.Logger) *asynqLogger { return &asynqLogger{l: l} }

func (a *asynqLogger) Debug(args ...interface{}) { a.l.Debug("asynq", "msg", args) }
func (a *asynqLogger) Info(args ...interface{})  { a.l.Info("asynq", "msg", args) }
func (a *asynqLogger) Warn(args ...interface{})  { a.l.Warn("asynq", "msg", args) }
func (a *asynqLogger) Error(args ...interface{}) { a.l.Error("asynq", "msg", args) }
func (a *asynqLogger) Fatal(args ...interface{}) { a.l.Error("asynq_fatal", "msg", args) }
