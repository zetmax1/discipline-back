package config

import (
	"strings"
	"testing"
)

const testJWTSecret = "0123456789abcdef0123456789abcdef" // 32 chars

// setRequiredEnv sets every required variable to a valid value.
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("JWT_SECRET", testJWTSecret)
	t.Setenv("DB_PASSWORD", "db-password-for-tests")
	t.Setenv("INTERNAL_JWT_SECRET", "fedcba9876543210fedcba9876543210")
	t.Setenv("PYTHON_SERVICE_URL", "http://discipline-py.railway.internal:8082")
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
	for _, name := range []string{"JWT_SECRET", "DB_PASSWORD", "INTERNAL_JWT_SECRET", "PYTHON_SERVICE_URL"} {
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
