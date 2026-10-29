package models

import "time"

// DefaultTimezone matches the users.timezone column default.
const DefaultTimezone = "Asia/Tashkent"

// Role constants
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// User represents a registered user.
type User struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	Name     string `gorm:"not null" json:"name"`
	Email    string `gorm:"uniqueIndex;not null" json:"email"`
	Password string `gorm:"not null" json:"-"` // excluded from JSON responses
	Role     string `gorm:"not null;default:user" json:"role"`
	Timezone string `gorm:"not null;default:Asia/Tashkent" json:"timezone"` // IANA name
	// TokenVersion is embedded in JWTs as "tv"; incrementing it revokes older tokens.
	TokenVersion int64     `gorm:"not null;default:0" json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// IsAdmin checks if the user has admin role.
func (u *User) IsAdmin() bool {
	return u.Role == RoleAdmin
}
