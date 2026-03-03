package config

import (
	"log"
	"os"
	"strconv"

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
}

// Load reads configuration from .env file and environment variables.
func Load() *Config {
	// Load .env file (ignore error if not present — env vars may be set directly)
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, reading from environment")
	}

	jwtExpiry, err := strconv.Atoi(getEnv("JWT_EXPIRY_HOURS", "72"))
	if err != nil {
		jwtExpiry = 72
	}

	return &Config{
		DBHost:       getEnv("DB_HOST", "localhost"),
		DBPort:       getEnv("DB_PORT", "5432"),
		DBUser:       getEnv("DB_USER", "postgres"),
		DBPassword:   getEnv("DB_PASSWORD", "postgres"),
		DBName:         getEnv("DB_NAME", "discipline"),
		JWTSecret:      getEnv("JWT_SECRET", "def3013nr2fewfy8923nfgsdjf24p29348r24pr8fhwufh2n34p8r24yrne8789)*(&f32fgef)"),
		JWTExpiryHrs:   jwtExpiry,
		Port:           getEnv("PORT", "8080"),
		AllowedOrigins: getEnv("ALLOWED_ORIGINS", "http://localhost:8081,https://discipline-front.up.railway.app"),
	}
}

// getEnv returns the value of an environment variable or a fallback default.
func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
