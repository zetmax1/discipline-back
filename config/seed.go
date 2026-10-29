package config

import (
	"log"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"discipline-backend/internal/models"
)

// SeedAdmin creates the first admin user if no admin exists in the database.
// Uses ADMIN_EMAIL and ADMIN_PASSWORD from .env (defaults provided).
func SeedAdmin(db *gorm.DB, cfg *Config) {
	// Check if any admin already exists
	var count int64
	db.Model(&models.User{}).Where("role = ?", models.RoleAdmin).Count(&count)
	if count > 0 {
		log.Println("Admin user already exists, skipping seed")
		return
	}

	email := getEnv("ADMIN_EMAIL", "admin@discipline.app")
	password := getEnv("ADMIN_PASSWORD", "admin123")
	name := getEnv("ADMIN_NAME", "Admin")

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("Failed to hash admin password: %v", err)
		return
	}

	admin := &models.User{
		Name:     name,
		Email:    email,
		Password: string(hashedPassword),
		Role:     models.RoleAdmin,
	}

	if err := db.Create(admin).Error; err != nil {
		log.Printf("Failed to seed admin user: %v", err)
		return
	}

	log.Printf("Admin user seeded: %s (%s)", admin.Name, admin.Email)
}
