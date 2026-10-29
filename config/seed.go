package config

import (
	"fmt"
	"log"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"discipline-backend/internal/models"
)

// minAdminPasswordLen is the minimum length for a seeded admin password.
const minAdminPasswordLen = 12

// adminSeedCredentials reads ADMIN_EMAIL and ADMIN_PASSWORD. ok is false (with a
// reason that never includes the password) unless both are set and the password
// is long enough.
func adminSeedCredentials() (email, password string, ok bool, reason string) {
	email = strings.TrimSpace(os.Getenv("ADMIN_EMAIL"))
	password = os.Getenv("ADMIN_PASSWORD")
	switch {
	case email == "" || password == "":
		return "", "", false, "ADMIN_EMAIL and ADMIN_PASSWORD are not both set"
	case len(password) < minAdminPasswordLen:
		return "", "", false, fmt.Sprintf("ADMIN_PASSWORD must be at least %d characters", minAdminPasswordLen)
	}
	return email, password, true, ""
}

// SeedAdmin creates the first admin user if no admin exists in the database.
// It only runs when ADMIN_EMAIL and ADMIN_PASSWORD are both set; otherwise it logs and skips.
func SeedAdmin(db *gorm.DB, cfg *Config) {
	email, password, ok, reason := adminSeedCredentials()
	if !ok {
		log.Printf("Skipping admin seed: %s", reason)
		return
	}

	// Check if any admin already exists
	var count int64
	db.Model(&models.User{}).Where("role = ?", models.RoleAdmin).Count(&count)
	if count > 0 {
		log.Println("Admin user already exists, skipping seed")
		return
	}

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
