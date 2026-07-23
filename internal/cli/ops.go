package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/redis/go-redis/v9"

	"growth-lms/internal/config"
	"growth-lms/internal/db"
)

// checkDeps pings Postgres and Redis with a short timeout and returns a
// per-dependency status map plus whether everything is healthy. It is the
// CLI-side counterpart to the HTTP readiness probe.
func checkDeps(cfg *config.Config) (map[string]string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	checks := map[string]string{}
	healthy := true

	pool, err := db.NewPool(ctx, cfg.Database.URL)
	if err != nil {
		checks["database"] = "unreachable: " + err.Error()
		healthy = false
	} else {
		defer pool.Close()
		if err := pool.Ping(ctx); err != nil {
			checks["database"] = "unreachable: " + err.Error()
			healthy = false
		} else {
			checks["database"] = "ok"
		}
	}

	if opt, err := redis.ParseURL(cfg.Redis.URL); err != nil {
		checks["redis"] = "invalid url: " + err.Error()
		healthy = false
	} else {
		rc := redis.NewClient(opt)
		defer rc.Close()
		if err := rc.Ping(ctx).Err(); err != nil {
			checks["redis"] = "unreachable: " + err.Error()
			healthy = false
		} else {
			checks["redis"] = "ok"
		}
	}
	return checks, healthy
}

func printChecks(checks map[string]string) {
	for _, name := range []string{"database", "redis"} {
		if status, ok := checks[name]; ok {
			fmt.Printf("  %-10s %s\n", name+":", status)
		}
	}
}

func runHealth(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	checks, healthy := checkDeps(cfg)
	printChecks(checks)
	if !healthy {
		return errors.New("one or more dependencies are unhealthy")
	}
	fmt.Println("healthy")
	return nil
}

func runStatus(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	if pid, running := serverProcessStatus(); running {
		fmt.Printf("server:     running (pid %d, pidfile %s)\n", pid, pidFilePath())
	} else {
		fmt.Printf("server:     not running (no live process for pidfile %s)\n", pidFilePath())
	}

	checks, healthy := checkDeps(cfg)
	fmt.Println("dependencies:")
	printChecks(checks)
	if !healthy {
		return errors.New("one or more dependencies are unhealthy")
	}
	return nil
}

// runBackup shells out to pg_dump in the custom (-Fc) format, which
// pg_restore can restore selectively and in parallel. The connection string
// comes from config so a backup can never target a different database than
// the running server.
func runBackup(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if _, err := exec.LookPath("pg_dump"); err != nil {
		return errors.New("pg_dump not found on PATH; install the PostgreSQL client tools")
	}

	out := defaultBackupPath()
	if len(args) > 0 && args[0] != "" {
		out = args[0]
	}

	cmd := exec.Command("pg_dump", "--format=custom", "--no-owner", "--no-acl", "--file", out, cfg.Database.URL)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("backing up database to %s ...\n", out)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_dump failed: %w", err)
	}
	fmt.Printf("backup written to %s\n", out)
	return nil
}

// runRestore restores a pg_dump custom-format archive. It is intentionally
// destructive-aware: --clean drops objects before recreating them, so the
// operator is warned this overwrites current data.
func runRestore(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if len(args) == 0 || args[0] == "" {
		return errors.New("restore: requires the path to a backup file")
	}
	path := args[0]
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("restore: cannot read %s: %w", path, err)
	}
	if _, err := exec.LookPath("pg_restore"); err != nil {
		return errors.New("pg_restore not found on PATH; install the PostgreSQL client tools")
	}

	fmt.Printf("WARNING: restoring %s into the configured database will overwrite existing objects.\n", path)
	cmd := exec.Command("pg_restore", "--clean", "--if-exists", "--no-owner", "--no-acl", "--dbname", cfg.Database.URL, path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// pg_restore exits non-zero on ignorable "does not exist" notices
		// from --clean; surface the error but make the cause clear.
		return fmt.Errorf("pg_restore reported errors (some --clean drop notices are expected on a fresh database): %w", err)
	}
	fmt.Println("restore complete")
	return nil
}

func defaultBackupPath() string {
	// Callers can pass an explicit path; the default is deterministic per
	// process rather than timestamped because the CLI has no wall clock
	// dependency injected and operators typically name backups themselves.
	if dir := os.Getenv("LMS_BACKUP_DIR"); dir != "" {
		return dir + "/growth-lms.dump"
	}
	return "growth-lms.dump"
}
