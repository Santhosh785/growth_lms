// Package config loads and validates typed application configuration from
// environment variables (and an optional .env file in development).
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Env identifies the deployment profile the application is running under.
type Env string

const (
	EnvDevelopment Env = "development"
	EnvStaging     Env = "staging"
	EnvProduction  Env = "production"
)

func (e Env) valid() bool {
	switch e {
	case EnvDevelopment, EnvStaging, EnvProduction:
		return true
	}
	return false
}

// Config is the fully validated, typed configuration for the application.
// It is loaded once at startup; nothing in the codebase should read
// os.Getenv directly outside this package.
type Config struct {
	Env     Env
	Port    int
	BaseURL string

	Database DatabaseConfig
	Supabase SupabaseConfig
	Redis    RedisConfig
	BunnyNet BunnyNetConfig
	Resend   ResendConfig
	Razorpay RazorpayConfig
	AI       AIConfig

	CORS        CORSConfig
	TrustProxy  bool
	LogHumanFmt bool
}

type DatabaseConfig struct {
	URL string
}

type SupabaseConfig struct {
	URL            string
	AnonKey        string
	ServiceRoleKey string
	StorageBucket  string
	JWTSecret      string
}

type RedisConfig struct {
	URL string
}

type BunnyNetConfig struct {
	APIKey        string
	StorageZone   string
	CDNURL        string
	WebhookSecret string
}

type ResendConfig struct {
	APIKey string
	// FromEmail is the sender address used for all Task 5 notification
	// emails. Not part of the `required` list below (Task 2/3 never wired
	// this up) — it has a sane development default and can be overridden
	// per-environment without affecting existing config validation tests.
	FromEmail string
}

type RazorpayConfig struct {
	KeyID     string
	KeySecret string
	// WebhookSecret verifies the signature on incoming Razorpay webhook
	// events (order/payment status changes). Enrollment/payment access
	// must only ever be granted from a verified webhook, never a browser
	// return URL — see plan.md's tenancy/commerce non-negotiables.
	WebhookSecret string
}

// Note: the stale-order-abandon timeout (how long a pending order is left
// before being swept and marked abandoned) is not a config field — it
// follows the publishSweepInterval precedent in internal/worker/worker.go,
// which hardcodes its sweep interval as an unexported const rather than an
// env-configurable value.

// AIConfig configures Task 9's AI authoring & tutor module. It is entirely
// optional (none of these are in the `required` list): with Enabled false —
// the default — the platform ships the stub provider, so the feature is
// dark until an operator turns it on. Provider "anthropic" needs APIKey.
// MonthlyTokenLimit is the platform-wide default per-org cap; an org may
// override it downward via organizations.ai_monthly_token_limit.
type AIConfig struct {
	Enabled           bool
	Provider          string
	APIKey            string
	Model             string
	MonthlyTokenLimit int64
}

type CORSConfig struct {
	AllowedOrigins []string
}

// requiredVar records a single environment variable that must be present,
// along with an optional validator run against its value.
type requiredVar struct {
	name     string
	validate func(string) error
}

// Load reads configuration from the environment (and, if present, a .env
// file), validates it, and returns a Config. It fails fast with a
// descriptive (but secret-free) error if anything required is missing or
// malformed.
func Load() (*Config, error) {
	// .env is optional: in production, real environment variables are set
	// by the deployment platform and no .env file will exist.
	_ = godotenv.Load()

	env := Env(getEnv("LMS_ENV", string(EnvDevelopment)))
	if !env.valid() {
		return nil, fmt.Errorf("config: LMS_ENV must be one of development, staging, production (got %q)", string(env))
	}

	var errs []string

	required := []requiredVar{
		{"LMS_DATABASE_URL", validateURL},
		{"LMS_SUPABASE_URL", validateURL},
		{"LMS_SUPABASE_ANON_KEY", nil},
		{"LMS_SUPABASE_SERVICE_ROLE_KEY", nil},
		{"LMS_SUPABASE_STORAGE_BUCKET", nil},
		{"LMS_SUPABASE_JWT_SECRET", nil},
		{"LMS_REDIS_URL", validateURL},
		{"LMS_BUNNY_API_KEY", nil},
		{"LMS_BUNNY_STORAGE_ZONE", nil},
		{"LMS_BUNNY_CDN_URL", validateURL},
		{"LMS_BUNNY_WEBHOOK_SECRET", nil},
		{"LMS_RESEND_API_KEY", nil},
		{"LMS_RAZORPAY_KEY_ID", nil},
		{"LMS_RAZORPAY_KEY_SECRET", nil},
		{"LMS_RAZORPAY_WEBHOOK_SECRET", nil},
		{"LMS_BASE_URL", validateURL},
	}

	values := map[string]string{}
	for _, rv := range required {
		v := os.Getenv(rv.name)
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Sprintf("%s is required", rv.name))
			continue
		}
		if rv.validate != nil {
			if err := rv.validate(v); err != nil {
				errs = append(errs, fmt.Sprintf("%s is invalid: %v", rv.name, err))
				continue
			}
		}
		values[rv.name] = v
	}

	portStr := getEnv("LMS_PORT", "8080")
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		errs = append(errs, fmt.Sprintf("LMS_PORT must be a valid port number (got %q)", portStr))
	}

	origins := splitAndTrim(getEnv("LMS_CORS_ALLOWED_ORIGINS", ""))
	if env == EnvProduction && len(origins) == 0 {
		errs = append(errs, "LMS_CORS_ALLOWED_ORIGINS is required in production")
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("config: invalid configuration:\n  - %s", strings.Join(errs, "\n  - "))
	}

	cfg := &Config{
		Env:     env,
		Port:    port,
		BaseURL: values["LMS_BASE_URL"],
		Database: DatabaseConfig{
			URL: values["LMS_DATABASE_URL"],
		},
		Supabase: SupabaseConfig{
			URL:            values["LMS_SUPABASE_URL"],
			AnonKey:        values["LMS_SUPABASE_ANON_KEY"],
			ServiceRoleKey: values["LMS_SUPABASE_SERVICE_ROLE_KEY"],
			StorageBucket:  values["LMS_SUPABASE_STORAGE_BUCKET"],
			JWTSecret:      values["LMS_SUPABASE_JWT_SECRET"],
		},
		Redis: RedisConfig{
			URL: values["LMS_REDIS_URL"],
		},
		BunnyNet: BunnyNetConfig{
			APIKey:        values["LMS_BUNNY_API_KEY"],
			StorageZone:   values["LMS_BUNNY_STORAGE_ZONE"],
			CDNURL:        values["LMS_BUNNY_CDN_URL"],
			WebhookSecret: values["LMS_BUNNY_WEBHOOK_SECRET"],
		},
		Resend: ResendConfig{
			APIKey:    values["LMS_RESEND_API_KEY"],
			FromEmail: getEnv("LMS_RESEND_FROM_EMAIL", "notifications@growth-lms.example"),
		},
		Razorpay: RazorpayConfig{
			KeyID:         values["LMS_RAZORPAY_KEY_ID"],
			KeySecret:     values["LMS_RAZORPAY_KEY_SECRET"],
			WebhookSecret: values["LMS_RAZORPAY_WEBHOOK_SECRET"],
		},
		AI: AIConfig{
			Enabled:  getEnvBool("LMS_AI_ENABLED", false),
			Provider: getEnv("LMS_AI_PROVIDER", "stub"),
			// ANTHROPIC_API_KEY is the name the Go SDK itself reads, so we
			// accept it directly and fall back to the LMS-namespaced form.
			APIKey:            getEnv("LMS_AI_API_KEY", os.Getenv("ANTHROPIC_API_KEY")),
			Model:             getEnv("LMS_AI_MODEL", "claude-opus-4-8"),
			MonthlyTokenLimit: getEnvInt64("LMS_AI_MONTHLY_TOKEN_LIMIT", 2_000_000),
		},
		CORS: CORSConfig{
			AllowedOrigins: origins,
		},
		TrustProxy:  getEnvBool("LMS_TRUST_PROXY", env != EnvDevelopment),
		LogHumanFmt: getEnvBool("LMS_LOG_HUMAN_FORMAT", env == EnvDevelopment),
	}

	return cfg, nil
}

// Redacted returns a copy of the config with all secret fields masked, safe
// to include in startup logs.
func (c *Config) Redacted() map[string]any {
	return map[string]any{
		"env":              c.Env,
		"port":             c.Port,
		"base_url":         c.BaseURL,
		"database_url":     redact(c.Database.URL),
		"supabase_url":     c.Supabase.URL,
		"redis_url":        redact(c.Redis.URL),
		"bunny_storage":    c.BunnyNet.StorageZone,
		"bunny_cdn_url":    c.BunnyNet.CDNURL,
		"cors_origins":     c.CORS.AllowedOrigins,
		"trust_proxy":      c.TrustProxy,
		"log_human_format": c.LogHumanFmt,
	}
}

func redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[redacted]"
	}
	u.User = nil
	return u.String()
}

func validateURL(v string) error {
	u, err := url.Parse(v)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("must be a valid absolute URL")
	}
	return nil
}

func getEnv(name, fallback string) string {
	if v, ok := os.LookupEnv(name); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

func getEnvBool(name string, fallback bool) bool {
	v, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func getEnvInt64(name string, fallback int64) int64 {
	v, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func splitAndTrim(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
