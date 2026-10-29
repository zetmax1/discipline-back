package config

import (
	"fmt"
	"log"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"discipline-backend/internal/models"
)

// postgresDSN builds the connection string; sslmode comes from DB_SSLMODE (default require).
func postgresDSN(cfg *Config) string {
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode,
	)
}

// ConnectDB opens a GORM connection to PostgreSQL and auto-migrates models.
func ConnectDB(cfg *Config) *gorm.DB {
	db, err := gorm.Open(postgres.Open(postgresDSN(cfg)), &gorm.Config{})
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
