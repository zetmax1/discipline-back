package config

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

// Config holds all configuration for the application.
type Config struct {
	DBHost         string
	DBPort         string
	DBUser         string
	DBPassword     string
	DBName         string
	DBSSLMode      string
	JWTSecret      string
	Port           string
	AllowedOrigins string
	// InternalJWTSecret signs the short-lived tokens Gin sends to the Python service.
	InternalJWTSecret string
	PythonServiceURL  string
	// TrustedProxies are proxy IPs/CIDRs whose X-Forwarded-For is honoured; empty trusts none.
	TrustedProxies []string
	// TrustedIPHeader, if set, is a platform header that always carries the real client IP.
	TrustedIPHeader string
	// AppEnv is "development" for local work; anything else runs Gin in release mode.
	AppEnv string
	// EnableDocs serves Swagger UI when ENABLE_DOCS=true.
	EnableDocs bool
	// EnableIPDebug serves GET /debug/ip when ENABLE_IP_DEBUG=true (staging only).
	EnableIPDebug bool
	// TurnstileSecret verifies Cloudflare Turnstile tokens on registration and throttled logins.
	TurnstileSecret string
	// SentryDSN enables error reporting to Sentry when set; SentryEnvironment labels events.
	SentryDSN         string
	SentryEnvironment string
	// RedisURL holds the shared rate-limit and login-throttle state (redis:// or rediss://).
	RedisURL string
	// Features are the ids switched on with FEATURE_<id>=true, e.g. FEATURE_F12A → "F12A".
	Features []string
}

// GinMode returns gin's debug mode only when APP_ENV=development.
func (c *Config) GinMode() string {
	if c.AppEnv == "development" {
		return "debug"
	}
	return "release"
}

// sslModes are the libpq sslmode values accepted in DB_SSLMODE.
var sslModes = []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}

// minJWTSecretLen is the minimum length accepted for JWT signing secrets.
const minJWTSecretLen = 32

// Load reads configuration from .env file and environment variables.
// It returns an error naming every required secret that is missing or invalid.
func Load() (*Config, error) {
	// Load .env file (ignore error if not present — env vars may be set directly)
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, reading from environment")
	}

	var problems []string
	jwtSecret := requireEnv("JWT_SECRET", &problems)
	if jwtSecret != "" && len(jwtSecret) < minJWTSecretLen {
		problems = append(problems, fmt.Sprintf("JWT_SECRET must be at least %d characters", minJWTSecretLen))
	}
	dbPassword := requireEnv("DB_PASSWORD", &problems)
	internalJWTSecret := requireEnv("INTERNAL_JWT_SECRET", &problems)
	if internalJWTSecret != "" && len(internalJWTSecret) < minJWTSecretLen {
		problems = append(problems, fmt.Sprintf("INTERNAL_JWT_SECRET must be at least %d characters", minJWTSecretLen))
	}
	if internalJWTSecret != "" && internalJWTSecret == jwtSecret {
		problems = append(problems, "INTERNAL_JWT_SECRET must be different from JWT_SECRET")
	}
	pythonServiceURL := requireEnv("PYTHON_SERVICE_URL", &problems)
	redisURL := requireEnv("REDIS_URL", &problems)
	turnstileSecret := requireEnv("TURNSTILE_SECRET", &problems)
	if redisURL != "" {
		if _, err := redis.ParseURL(redisURL); err != nil {
			problems = append(problems, "REDIS_URL must be a redis:// or rediss:// URL")
		}
	}
	dbSSLMode := strings.TrimSpace(os.Getenv("DB_SSLMODE"))
	if dbSSLMode == "" {
		dbSSLMode = "require"
	}
	if !slices.Contains(sslModes, dbSSLMode) {
		problems = append(problems, "DB_SSLMODE must be one of: "+strings.Join(sslModes, ", "))
	}
	trustedProxies := splitList(os.Getenv("TRUSTED_PROXIES"))
	for _, p := range trustedProxies {
		if !validProxy(p) {
			problems = append(problems, fmt.Sprintf("TRUSTED_PROXIES entry %q is not an IP or CIDR", p))
		}
	}
	if len(problems) > 0 {
		return nil, errors.New("invalid configuration: " + strings.Join(problems, "; "))
	}

	return &Config{
		DBHost:            getEnv("DB_HOST", "localhost"),
		DBPort:            getEnv("DB_PORT", "5432"),
		DBUser:            getEnv("DB_USER", "postgres"),
		DBPassword:        dbPassword,
		DBName:            getEnv("DB_NAME", "discipline"),
		DBSSLMode:         dbSSLMode,
		JWTSecret:         jwtSecret,
		Port:              getEnv("PORT", "8080"),
		AllowedOrigins:    getEnv("ALLOWED_ORIGINS", "http://localhost:8081,http://localhost:8082,http://localhost:8083,http://localhost:5173,https://discipline-front.up.railway.app,https://accurate-wisdom-production-cda6.up.railway.app/"),
		InternalJWTSecret: internalJWTSecret,
		PythonServiceURL:  pythonServiceURL,
		TrustedProxies:    trustedProxies,
		TrustedIPHeader:   strings.TrimSpace(os.Getenv("TRUSTED_IP_HEADER")),
		AppEnv:            strings.TrimSpace(os.Getenv("APP_ENV")),
		EnableDocs:        strings.EqualFold(strings.TrimSpace(os.Getenv("ENABLE_DOCS")), "true"),
		EnableIPDebug:     strings.EqualFold(strings.TrimSpace(os.Getenv("ENABLE_IP_DEBUG")), "true"),
		RedisURL:          redisURL,
		TurnstileSecret:   turnstileSecret,
		SentryDSN:         strings.TrimSpace(os.Getenv("SENTRY_DSN")),
		SentryEnvironment: strings.TrimSpace(os.Getenv("SENTRY_ENVIRONMENT")),
		Features:          EnabledFeatures(os.Environ()),
	}, nil
}

// requireEnv returns a required variable, recording a problem if it is unset or empty.
func requireEnv(key string, problems *[]string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		*problems = append(*problems, key+" is required")
	}
	return value
}

// splitList splits a comma-separated value, trimming spaces and dropping empty items.
func splitList(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// featureVar matches a feature flag variable: FEATURE_ + an id such as F1, F12A, F9B.
var featureVar = regexp.MustCompile(`^FEATURE_(F[0-9]+[A-Z]?)$`)

// EnabledFeatures returns the sorted ids of the FEATURE_<id> variables set to "true".
// Any other value, or an unset variable, leaves the feature off.
func EnabledFeatures(environ []string) []string {
	enabled := []string{}
	for _, kv := range environ {
		key, value, _ := strings.Cut(kv, "=")
		if m := featureVar.FindStringSubmatch(key); m != nil && strings.EqualFold(strings.TrimSpace(value), "true") {
			enabled = append(enabled, m[1])
		}
	}
	slices.Sort(enabled)
	return enabled
}

// validProxy reports whether s is an IP address or CIDR range.
func validProxy(s string) bool {
	if net.ParseIP(s) != nil {
		return true
	}
	_, _, err := net.ParseCIDR(s)
	return err == nil
}

// getEnv returns the value of an environment variable or a fallback default.
func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
