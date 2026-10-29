package config

import (
	"errors"
	"fmt"
	"log"
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
	AnalyticsAPIKey string
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
	analyticsAPIKey := requireEnv("ANALYTICS_API_KEY", &problems)
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
		AnalyticsAPIKey: analyticsAPIKey,
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

// getEnv returns the value of an environment variable or a fallback default.
func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
