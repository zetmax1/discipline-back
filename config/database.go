package config

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"discipline-backend/internal/dbmigrate"
)

// postgresDSN builds the connection string; sslmode comes from DB_SSLMODE (default require).
func postgresDSN(cfg *Config) string {
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode,
	)
}

// slogWriter sends GORM's lines to a structured logger at WARN level (GORM only
// logs warnings, errors and slow queries at the configured level).
type slogWriter struct{ l *slog.Logger }

func (w slogWriter) Printf(format string, args ...any) {
	w.l.Warn(strings.TrimSpace(fmt.Sprintf(format, args...)), "source", "gorm")
}

// newGormLogger logs warnings and errors only, never logs "record not found",
// and prints SQL without bound values so emails and other inputs stay out of logs.
func newGormLogger(l *slog.Logger) logger.Interface {
	return logger.New(slogWriter{l}, logger.Config{
		SlowThreshold:             200 * time.Millisecond,
		LogLevel:                  logger.Warn,
		IgnoreRecordNotFoundError: true,
		ParameterizedQueries:      true,
		Colorful:                  false,
	})
}

// configurePool bounds the connection pool (Railway Postgres allows ~100 connections in total).
func configurePool(db *sql.DB) {
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
}

// ConnectDB opens a GORM connection to PostgreSQL and verifies the schema is fully migrated.
func ConnectDB(cfg *Config) *gorm.DB {
	db, err := gorm.Open(postgres.Open(postgresDSN(cfg)), &gorm.Config{Logger: newGormLogger(slog.Default())})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	log.Println("Database connected successfully")

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("Failed to access database pool: %v", err)
	}
	configurePool(sqlDB)

	// Schema changes are applied with the migrate command, never at startup.
	if err := dbmigrate.RequireLatest(context.Background(), sqlDB); err != nil {
		log.Fatalf("Database schema check failed: %v", err)
	}

	return db
}
