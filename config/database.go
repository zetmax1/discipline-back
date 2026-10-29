package config

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"discipline-backend/internal/models"
)

// postgresDSN builds the connection string; sslmode comes from DB_SSLMODE (default require).
func postgresDSN(cfg *Config) string {
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode,
	)
}

// newGormLogger logs warnings and errors only, never logs "record not found",
// and prints SQL without bound values so emails and other inputs stay out of logs.
func newGormLogger(w io.Writer) logger.Interface {
	return logger.New(log.New(w, "\r\n", log.LstdFlags), logger.Config{
		SlowThreshold:             200 * time.Millisecond,
		LogLevel:                  logger.Warn,
		IgnoreRecordNotFoundError: true,
		ParameterizedQueries:      true,
		Colorful:                  false,
	})
}

// ConnectDB opens a GORM connection to PostgreSQL and auto-migrates models.
func ConnectDB(cfg *Config) *gorm.DB {
	db, err := gorm.Open(postgres.Open(postgresDSN(cfg)), &gorm.Config{Logger: newGormLogger(os.Stdout)})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	log.Println("Database connected successfully")

	// Auto-migrate models
	if err := db.AutoMigrate(&models.User{}, &models.Task{}, &models.VocabularyWord{}); err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}

	log.Println("Database migration completed")

	return db
}
