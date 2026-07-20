// Package worker runs the Redis-backed background job consumer (asynq).
// It shares the same binary, config, and dependency wiring as the API
// server; only the entrypoint command differs.
package worker

import (
	"log/slog"

	"github.com/hibiken/asynq"

	"growth-lms/internal/config"
)

// Queue names. Task 5/6 register real task handlers (email sends, webhook
// processing) against this mux as those domains land.
const (
	QueueDefault  = "default"
	QueueCritical = "critical"
)

// Run starts the asynq server and blocks until it shuts down or ctx is
// canceled. redisAddr/opts come from cfg.Redis.URL parsed by the caller.
func Run(cfg *config.Config, redisOpt asynq.RedisConnOpt, logger *slog.Logger) error {
	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: 10,
		Queues: map[string]int{
			QueueCritical: 6,
			QueueDefault:  4,
		},
		Logger: newAsynqLogger(logger),
	})

	mux := asynq.NewServeMux()
	// Task-specific handlers register themselves here in later tasks, e.g.:
	//   mux.HandleFunc(email.TypeSendWelcome, email.HandleSendWelcome)

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
