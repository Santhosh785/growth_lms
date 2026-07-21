package config

import (
	"os"
	"testing"
)

func setValidEnv(t *testing.T) {
	t.Helper()
	vars := map[string]string{
		"LMS_ENV":                       "development",
		"LMS_DATABASE_URL":              "postgres://user:pass@localhost:54322/postgres",
		"LMS_SUPABASE_URL":              "http://localhost:54321",
		"LMS_SUPABASE_ANON_KEY":         "anon-key",
		"LMS_SUPABASE_SERVICE_ROLE_KEY": "service-key",
		"LMS_SUPABASE_STORAGE_BUCKET":   "course-media",
		"LMS_SUPABASE_JWT_SECRET":       "test-jwt-secret",
		"LMS_REDIS_URL":                 "redis://localhost:6379",
		"LMS_BUNNY_API_KEY":             "bunny-key",
		"LMS_BUNNY_STORAGE_ZONE":        "lms-dev",
		"LMS_BUNNY_CDN_URL":             "https://lms-dev.b-cdn.net",
		"LMS_BUNNY_WEBHOOK_SECRET":      "bunny-webhook-secret",
		"LMS_RESEND_API_KEY":            "resend-key",
		"LMS_RAZORPAY_KEY_ID":           "rzp_test_id",
		"LMS_RAZORPAY_KEY_SECRET":       "rzp_test_secret",
		"LMS_RAZORPAY_WEBHOOK_SECRET":   "rzp-webhook-secret",
		"LMS_BASE_URL":                  "http://localhost:8080",
		"LMS_PORT":                      "8080",
	}
	for k, v := range vars {
		t.Setenv(k, v)
	}
}

func TestLoad_Valid(t *testing.T) {
	setValidEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if cfg.Env != EnvDevelopment {
		t.Errorf("expected development env, got %s", cfg.Env)
	}
	if cfg.Port != 8080 {
		t.Errorf("expected port 8080, got %d", cfg.Port)
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	setValidEnv(t)
	os.Unsetenv("LMS_DATABASE_URL")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing LMS_DATABASE_URL")
	}
}

func TestLoad_InvalidURL(t *testing.T) {
	setValidEnv(t)
	t.Setenv("LMS_DATABASE_URL", "not-a-url")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid LMS_DATABASE_URL")
	}
}

func TestLoad_InvalidPort(t *testing.T) {
	setValidEnv(t)
	t.Setenv("LMS_PORT", "not-a-port")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid LMS_PORT")
	}
}

func TestLoad_InvalidEnv(t *testing.T) {
	setValidEnv(t)
	t.Setenv("LMS_ENV", "bogus")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid LMS_ENV")
	}
}

func TestLoad_MissingBunnyWebhookSecret(t *testing.T) {
	setValidEnv(t)
	os.Unsetenv("LMS_BUNNY_WEBHOOK_SECRET")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing LMS_BUNNY_WEBHOOK_SECRET")
	}
}

func TestLoad_MissingRazorpayWebhookSecret(t *testing.T) {
	setValidEnv(t)
	os.Unsetenv("LMS_RAZORPAY_WEBHOOK_SECRET")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing LMS_RAZORPAY_WEBHOOK_SECRET")
	}
}

func TestLoad_ProductionRequiresCORSOrigins(t *testing.T) {
	setValidEnv(t)
	t.Setenv("LMS_ENV", "production")
	os.Unsetenv("LMS_CORS_ALLOWED_ORIGINS")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing CORS origins in production")
	}
}

func TestRedacted_DoesNotExposeSecrets(t *testing.T) {
	setValidEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	redacted := cfg.Redacted()
	dbURL, _ := redacted["database_url"].(string)
	if dbURL == "" {
		t.Fatal("expected database_url in redacted output")
	}
	if contains(dbURL, "pass") {
		t.Errorf("expected credentials to be redacted, got %q", dbURL)
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
