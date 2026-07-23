// Command app is the single Growth LMS binary. The long-running services —
// `serve` (HTTP API/HTML server) and `worker` (Redis-backed job consumer) —
// share config/dependency wiring here. The operational commands (setup,
// migrate, backup, restore, health, status, start, stop, logs) are
// dispatched to internal/cli so this entry point stays small.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	"growth-lms/internal/cli"
	"growth-lms/internal/config"
	"growth-lms/internal/db"
	"growth-lms/internal/httpserver"
	"growth-lms/internal/logging"
	"growth-lms/internal/worker"
)

// serviceCommands run a long-lived process and own the config+logger wiring
// below. Everything else is an operational command handled by internal/cli.
var serviceCommands = []string{"serve", "worker"}

func main() {
	if len(os.Args) < 2 {
		cli.Usage(os.Stderr, serviceCommands)
		os.Exit(1)
	}

	command := os.Args[1]

	// Operational commands (migrate/backup/health/...) load their own config
	// and are not tied to the service logger, so dispatch them first.
	if command != "serve" && command != "worker" {
		if cmd, ok := cli.Registry()[command]; ok {
			if err := cmd.Run(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", command, err)
				os.Exit(1)
			}
			return
		}
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", command)
		cli.Usage(os.Stderr, serviceCommands)
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	level := slog.LevelInfo
	if cfg.Env == config.EnvDevelopment {
		level = slog.LevelDebug
	}
	logger := logging.New(cfg.LogHumanFmt, level)
	// Stage 7: handler-level best-effort notification-enqueue failures log
	// via slog.Default() rather than threading a *slog.Logger through
	// AuthDeps for one non-critical log line — SetDefault makes those
	// calls actually route through the configured handler/format instead
	// of slog's bare fallback.
	slog.SetDefault(logger)
	logger.Info("starting", "command", command, "config", cfg.Redacted())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch command {
	case "serve":
		if err := runServe(ctx, cfg, logger); err != nil {
			logger.Error("serve exited with error", "error", err)
			os.Exit(1)
		}
	case "worker":
		if err := runWorker(cfg, logger); err != nil {
			logger.Error("worker exited with error", "error", err)
			os.Exit(1)
		}
	}
}

func runServe(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	pool, err := db.NewPool(ctx, cfg.Database.URL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()

	redisClient := newRedisClient(cfg)
	defer redisClient.Close()

	engine := httpserver.New(cfg, logger, pool, redisClient)

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           engine,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutting down http server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func runWorker(cfg *config.Config, logger *slog.Logger) error {
	redisOpt, err := asynq.ParseRedisURI(cfg.Redis.URL)
	if err != nil {
		return fmt.Errorf("parse redis url: %w", err)
	}
	return worker.Run(cfg, redisOpt, logger)
}

func newRedisClient(cfg *config.Config) *redis.Client {
	opt, err := redis.ParseURL(cfg.Redis.URL)
	if err != nil {
		// Config validation already confirmed this is a well-formed URL;
		// a parse failure here means the scheme isn't redis:// or rediss://.
		panic(fmt.Sprintf("invalid LMS_REDIS_URL: %v", err))
	}
	return redis.NewClient(opt)
}
