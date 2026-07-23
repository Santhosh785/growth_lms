package cli

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/golang-migrate/migrate/v4"
	// Registers the "pgx5" database driver golang-migrate resolves from the
	// URL scheme, and the "file://" source driver for on-disk .sql files.
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"growth-lms/internal/config"
)

// migrationsDir locates db/migrations relative to the working directory,
// falling back to LMS_MIGRATIONS_DIR so the command works from a deployed
// layout where the binary and migrations sit elsewhere.
func migrationsDir() (string, error) {
	if override := os.Getenv("LMS_MIGRATIONS_DIR"); override != "" {
		return override, nil
	}
	for _, candidate := range []string{"db/migrations", "../db/migrations", "../../db/migrations"} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			abs, err := filepath.Abs(candidate)
			if err != nil {
				return "", err
			}
			return abs, nil
		}
	}
	return "", errors.New("could not locate db/migrations (set LMS_MIGRATIONS_DIR)")
}

// newMigrator builds a golang-migrate instance pointed at the configured
// database. The pgx driver registers itself under the "pgx5" scheme, so the
// postgres:// URL from config is rewritten to match.
func newMigrator(cfg *config.Config) (*migrate.Migrate, error) {
	dir, err := migrationsDir()
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(cfg.Database.URL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	u.Scheme = "pgx5"
	m, err := migrate.New("file://"+dir, u.String())
	if err != nil {
		return nil, fmt.Errorf("init migrator: %w", err)
	}
	return m, nil
}

func runMigrate(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	action := "up"
	if len(args) > 0 {
		action = args[0]
	}

	m, err := newMigrator(cfg)
	if err != nil {
		return err
	}
	defer m.Close()

	switch action {
	case "up":
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return fmt.Errorf("migrate up: %w", err)
		}
		fmt.Println("migrations applied")
	case "down":
		steps := 1
		if len(args) > 1 {
			n, err := strconv.Atoi(args[1])
			if err != nil || n < 1 {
				return fmt.Errorf("migrate down: expected a positive step count, got %q", args[1])
			}
			steps = n
		}
		if err := m.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return fmt.Errorf("migrate down: %w", err)
		}
		fmt.Printf("reverted %d migration(s)\n", steps)
	case "version":
		v, dirty, err := m.Version()
		if errors.Is(err, migrate.ErrNilVersion) {
			fmt.Println("no migrations applied")
			return nil
		}
		if err != nil {
			return fmt.Errorf("migrate version: %w", err)
		}
		state := "clean"
		if dirty {
			state = "DIRTY (a prior migration failed midway; fix the schema, then `migrate force <version>`)"
		}
		fmt.Printf("version %d (%s)\n", v, state)
	case "force":
		if len(args) < 2 {
			return errors.New("migrate force: requires a version number")
		}
		v, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("migrate force: invalid version %q", args[1])
		}
		if err := m.Force(v); err != nil {
			return fmt.Errorf("migrate force: %w", err)
		}
		fmt.Printf("forced version to %d (dirty flag cleared)\n", v)
	default:
		return fmt.Errorf("migrate: unknown action %q (want up|down|version|force)", action)
	}
	return nil
}
