package config

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"gorm.io/gorm"
)

const testJWTSecret = "0123456789abcdef0123456789abcdef" // 32 chars

// setRequiredEnv sets every required variable to a valid value.
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("JWT_SECRET", testJWTSecret)
	t.Setenv("DB_PASSWORD", "db-password-for-tests")
	t.Setenv("INTERNAL_JWT_SECRET", "fedcba9876543210fedcba9876543210")
	t.Setenv("PYTHON_SERVICE_URL", "http://discipline-py.railway.internal:8082")
	t.Setenv("REDIS_URL", "redis://redis.railway.internal:6379/0")
}

func TestLoad_SucceedsWithRequiredSecrets(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected config to load, got %v", err)
	}
	if cfg.JWTSecret != testJWTSecret {
		t.Fatalf("JWTSecret not read from env")
	}
}

func TestLoad_FailsWhenSecretMissing(t *testing.T) {
	for _, name := range []string{"JWT_SECRET", "DB_PASSWORD", "INTERNAL_JWT_SECRET", "PYTHON_SERVICE_URL", "REDIS_URL"} {
		t.Run(name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv(name, "")
			_, err := Load()
			if err == nil {
				t.Fatalf("expected an error when %s is missing", name)
			}
			if !strings.Contains(err.Error(), name) {
				t.Fatalf("error should name the missing variable %s, got %q", name, err)
			}
		})
	}
}

func TestLoad_FailsWhenJWTSecretTooShort(t *testing.T) {
	setRequiredEnv(t)
	short := strings.Repeat("x", 31)
	t.Setenv("JWT_SECRET", short)
	_, err := Load()
	if err == nil {
		t.Fatal("expected an error for a 31-char JWT_SECRET")
	}
	if strings.Contains(err.Error(), short) {
		t.Fatal("error message must not contain the secret value")
	}
}

func TestLoad_FailsWhenInternalJWTSecretTooShort(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("INTERNAL_JWT_SECRET", strings.Repeat("y", 31))
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "INTERNAL_JWT_SECRET") {
		t.Fatalf("expected an INTERNAL_JWT_SECRET length error, got %v", err)
	}
}

func TestLoad_FailsWhenInternalSecretEqualsJWTSecret(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("INTERNAL_JWT_SECRET", testJWTSecret)
	if _, err := Load(); err == nil {
		t.Fatal("INTERNAL_JWT_SECRET must be distinct from JWT_SECRET")
	}
}

func TestLoad_DoesNotFallBackToHardcodedSecret(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("JWT_SECRET", "")
	cfg, err := Load()
	if err == nil && cfg != nil && cfg.JWTSecret != "" {
		t.Fatal("JWT_SECRET must not fall back to a hardcoded default")
	}
}

func TestAdminSeedCredentials(t *testing.T) {
	cases := []struct {
		name, email, password string
		want                  bool
	}{
		{"both set, strong password", "admin@example.com", "correct-horse-battery", true},
		{"exactly 12 chars", "admin@example.com", "123456789012", true},
		{"email missing", "", "correct-horse-battery", false},
		{"password missing", "admin@example.com", "", false},
		{"password too short", "admin@example.com", "admin123", false},
		{"11 chars", "admin@example.com", "12345678901", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ADMIN_EMAIL", tc.email)
			t.Setenv("ADMIN_PASSWORD", tc.password)
			_, _, ok, reason := adminSeedCredentials()
			if ok != tc.want {
				t.Fatalf("expected ok=%v, got %v (reason %q)", tc.want, ok, reason)
			}
			if !ok && reason == "" {
				t.Fatal("a skipped seed must explain why")
			}
			if tc.password != "" && strings.Contains(reason, tc.password) {
				t.Fatal("reason must not contain the password")
			}
		})
	}
}

func TestLoad_TrustedProxies(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("TRUSTED_PROXIES", " 10.0.0.0/8, 192.168.1.1 ,")
	t.Setenv("TRUSTED_IP_HEADER", "X-Real-IP")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[0] != "10.0.0.0/8" || cfg.TrustedProxies[1] != "192.168.1.1" {
		t.Fatalf("unexpected proxies %v", cfg.TrustedProxies)
	}
	if cfg.TrustedIPHeader != "X-Real-IP" {
		t.Fatalf("unexpected header %q", cfg.TrustedIPHeader)
	}
}

func TestLoad_TrustedProxiesDefaultToNone(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("TRUSTED_PROXIES", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Fatalf("expected no trusted proxies, got %v", cfg.TrustedProxies)
	}
}

func TestLoad_RejectsInvalidTrustedProxy(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8,nope")
	if _, err := Load(); err == nil {
		t.Fatal("expected an error for an invalid TRUSTED_PROXIES entry")
	}
}

func TestLoad_DBSSLModeDefaultsToRequire(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("DB_SSLMODE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DBSSLMode != "require" {
		t.Fatalf("expected default sslmode require, got %q", cfg.DBSSLMode)
	}
	if !strings.Contains(postgresDSN(cfg), "sslmode=require") {
		t.Fatal("DSN must use sslmode=require by default")
	}
	if strings.Contains(postgresDSN(cfg), "sslmode=disable") {
		t.Fatal("DSN must not hardcode sslmode=disable")
	}
}

func TestLoad_DBSSLModeFromEnv(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("DB_SSLMODE", "verify-full")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(postgresDSN(cfg), "sslmode=verify-full") {
		t.Fatalf("DSN must use DB_SSLMODE, got sslmode=%s", cfg.DBSSLMode)
	}
}

func TestLoad_RejectsInvalidDBSSLMode(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("DB_SSLMODE", "maybe")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "DB_SSLMODE") {
		t.Fatalf("expected a DB_SSLMODE error, got %v", err)
	}
}

func TestLoad_AppEnvAndDocsFlags(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("APP_ENV", "")
	t.Setenv("ENABLE_DOCS", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.GinMode() != "release" {
		t.Fatalf("default gin mode must be release, got %s", cfg.GinMode())
	}
	if cfg.EnableDocs {
		t.Fatal("docs must be disabled by default")
	}

	t.Setenv("APP_ENV", "development")
	t.Setenv("ENABLE_DOCS", "true")
	cfg, _ = Load()
	if cfg.GinMode() != "debug" {
		t.Fatalf("APP_ENV=development must use debug mode, got %s", cfg.GinMode())
	}
	if !cfg.EnableDocs {
		t.Fatal("ENABLE_DOCS=true must enable docs")
	}

	t.Setenv("APP_ENV", "production")
	t.Setenv("ENABLE_DOCS", "1")
	cfg, _ = Load()
	if cfg.GinMode() != "release" || cfg.EnableDocs {
		t.Fatal("only APP_ENV=development and ENABLE_DOCS=true change the defaults")
	}
}

func TestGormLogger_WarnLevelAndQuiet(t *testing.T) {
	var buf bytes.Buffer
	l := newGormLogger(&buf)
	ctx := context.Background()

	l.Info(ctx, "info message %s", "x")
	l.Trace(ctx, time.Now(), func() (string, int64) {
		return `SELECT * FROM "users" WHERE email = 'person@example.com'`, 0
	}, gorm.ErrRecordNotFound)
	l.Trace(ctx, time.Now(), func() (string, int64) { return "SELECT 1", 1 }, nil)
	if buf.Len() != 0 {
		t.Fatalf("info, fast queries and record-not-found must not be logged, got %q", buf.String())
	}

	l.Warn(ctx, "warn message")
	if !strings.Contains(buf.String(), "warn message") {
		t.Fatal("warnings must still be logged")
	}
}

func TestConfigurePool(t *testing.T) {
	db, err := sql.Open("pgx", "postgres://invalid.invalid/db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	configurePool(db)
	if got := db.Stats().MaxOpenConnections; got != 10 {
		t.Fatalf("MaxOpenConns = %d, want 10", got)
	}
}

func TestLoad_RejectsInvalidRedisURL(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("REDIS_URL", "localhost:6379")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "REDIS_URL") {
		t.Fatalf("expected a REDIS_URL error, got %v", err)
	}
}
