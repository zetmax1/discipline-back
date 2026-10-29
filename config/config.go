package config

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all configuration for the application.
type Config struct {
	DBHost       string
	DBPort       string
	DBUser       string
	DBPassword   string
	DBName       string
	JWTSecret      string
	JWTExpiryHrs   int
	Port           string
	AllowedOrigins string
	// InternalJWTSecret signs the short-lived tokens Gin sends to the Python service.
	InternalJWTSecret string
	PythonServiceURL  string
	// TrustedProxies are proxy IPs/CIDRs whose X-Forwarded-For is honoured; empty trusts none.
	TrustedProxies []string
	// TrustedIPHeader, if set, is a platform header that always carries the real client IP.
	TrustedIPHeader string
}

// minJWTSecretLen is the minimum length accepted for JWT signing secrets.
const minJWTSecretLen = 32

// Load reads configuration from .env file and environment variables.
// It returns an error naming every required secret that is missing or invalid.
func Load() (*Config, error) {
	// Load .env file (ignore error if not present — env vars may be set directly)
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, reading from environment")
	}

	jwtExpiry, err := strconv.Atoi(getEnv("JWT_EXPIRY_HOURS", "72"))
	if err != nil {
		jwtExpiry = 72
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
		DBHost:       getEnv("DB_HOST", "localhost"),
		DBPort:       getEnv("DB_PORT", "5432"),
		DBUser:       getEnv("DB_USER", "postgres"),
		DBPassword:   dbPassword,
		DBName:         getEnv("DB_NAME", "discipline"),
		JWTSecret:      jwtSecret,
		JWTExpiryHrs:   jwtExpiry,
		Port:           getEnv("PORT", "8080"),
		AllowedOrigins: getEnv("ALLOWED_ORIGINS", "http://localhost:8081,http://localhost:8082,http://localhost:8083,http://localhost:5173,https://discipline-front.up.railway.app,https://accurate-wisdom-production-cda6.up.railway.app/"),
		InternalJWTSecret: internalJWTSecret,
		PythonServiceURL:  pythonServiceURL,
		TrustedProxies:    trustedProxies,
		TrustedIPHeader:   strings.TrimSpace(os.Getenv("TRUSTED_IP_HEADER")),
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
