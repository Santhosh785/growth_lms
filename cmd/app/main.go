// Command app is the single Growth LMS binary. It supports two
// subcommands sharing one config/dependency wiring: `serve` runs the
// HTTP API/HTML server, `worker` runs the Redis-backed job consumer.
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

	"growth-lms/internal/config"
	"growth-lms/internal/db"
	"growth-lms/internal/httpserver"
	"growth-lms/internal/logging"
	"growth-lms/internal/worker"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: app <serve|worker>")
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
	logger.Info("starting", "command", os.Args[1], "config", cfg.Redacted())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch os.Args[1] {
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
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q; usage: app <serve|worker>\n", os.Args[1])
		os.Exit(1)
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
