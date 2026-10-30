package services

import (
	"crypto/rand"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"discipline-backend/internal/apierr"
	"discipline-backend/internal/models"
	"discipline-backend/internal/repositories"
)

// ErrInvalidCredentials is returned by Login for an unknown email or wrong password.
var ErrInvalidCredentials = apierr.New(apierr.InvalidCredentials, "invalid email or password")

// ErrEmailTaken is returned when registering with an email that already exists.
var ErrEmailTaken = apierr.New(apierr.EmailTaken, "user with this email already exists")

// ErrPasswordPolicy is wrapped by every password policy violation.
var ErrPasswordPolicy = errors.New("password does not meet policy")

// Password policy: bcrypt only uses the first 72 bytes, so longer passwords are rejected.
const (
	minPasswordChars = 10
	maxPasswordBytes = 72
)

// ValidatePassword enforces the password policy for registration and password changes.
func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < minPasswordChars {
		return apierr.Wrap(ErrPasswordPolicy, apierr.PasswordTooShort, fmt.Sprintf("password must be at least %d characters", minPasswordChars))
	}
	if len(password) > maxPasswordBytes {
		return apierr.Wrap(ErrPasswordPolicy, apierr.PasswordTooLong, fmt.Sprintf("password must be at most %d bytes", maxPasswordBytes))
	}
	return nil
}

// ErrInvalidTimezone is returned for a time zone that is not a loadable IANA name.
var ErrInvalidTimezone = apierr.New(apierr.InvalidTimezone, "timezone must be a valid IANA time zone name, e.g. Asia/Tashkent")

// ValidateTimezone accepts IANA names that time.LoadLocation can load ("Local" is rejected).
func ValidateTimezone(tz string) error {
	if tz == "" || tz == "Local" {
		return ErrInvalidTimezone
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return ErrInvalidTimezone
	}
	return nil
}

// Profile update errors.
var (
	ErrUserNotFound             = apierr.New(apierr.UserNotFound, "user not found")
	ErrCurrentPasswordRequired  = apierr.New(apierr.CurrentPasswordRequired, "current password is required")
	ErrCurrentPasswordIncorrect = apierr.New(apierr.CurrentPasswordIncorrect, "current password is incorrect")
)

// compareHashAndPassword is a variable so tests can observe calls.
var compareHashAndPassword = bcrypt.CompareHashAndPassword

// dummyPasswordHash is compared against when the email is unknown, so a login
// for a missing account costs the same bcrypt work as one for a real account.
var dummyPasswordHash = func() []byte {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		panic(err)
	}
	hash, err := bcrypt.GenerateFromPassword(random, bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return hash
}()

// AuthService defines authentication business logic.
type AuthService interface {
	Register(name, email, password string) (*models.User, string, error)
	RegisterAdmin(name, email, password string) (*models.User, string, error)
	Login(email, password string) (*models.User, string, error)
	GetUserByID(id uint) (*models.User, error)
	// UpdateProfile returns a replacement token when the password changed (older tokens are revoked).
	UpdateProfile(id uint, name, email, oldPassword, newPassword, timezone string) (*models.User, string, error)
	// Logout revokes every token issued to the user so far.
	Logout(userID uint) error
}

// AccessTokenTTL is the lifetime of access tokens.
const AccessTokenTTL = 24 * time.Hour

type authService struct {
	userRepo  repositories.UserRepository
	jwtSecret string
}

// NewAuthService creates a new AuthService.
func NewAuthService(userRepo repositories.UserRepository, jwtSecret string) AuthService {
	return &authService{
		userRepo:  userRepo,
		jwtSecret: jwtSecret,
	}
}

// Register creates a new user with role "user".
func (s *authService) Register(name, email, password string) (*models.User, string, error) {
	return s.registerWithRole(name, email, password, models.RoleUser)
}

// RegisterAdmin creates a new user with role "admin".
func (s *authService) RegisterAdmin(name, email, password string) (*models.User, string, error) {
	return s.registerWithRole(name, email, password, models.RoleAdmin)
}

// registerWithRole is the shared registration logic.
func (s *authService) registerWithRole(name, email, password, role string) (*models.User, string, error) {
	if err := ValidatePassword(password); err != nil {
		return nil, "", err
	}

	// Check if user already exists
	existing, _ := s.userRepo.FindByEmail(email)
	if existing != nil {
		return nil, "", ErrEmailTaken
	}

	// Hash password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", errors.New("failed to hash password")
	}

	user := &models.User{
		Name:     name,
		Email:    email,
		Password: string(hashedPassword),
		Role:     role,
	}

	if err := s.userRepo.Create(user); err != nil {
		return nil, "", errors.New("failed to create user")
	}

	// Generate JWT
	token, err := s.generateToken(user.ID, user.Role, user.TokenVersion)
	if err != nil {
		return nil, "", errors.New("failed to generate token")
	}

	return user, token, nil
}

// Login authenticates a user and returns a JWT token.
func (s *authService) Login(email, password string) (*models.User, string, error) {
	user, err := s.userRepo.FindByEmail(email)
	if err != nil {
		// Same bcrypt cost as a real account, so response time doesn't reveal unknown emails.
		_ = compareHashAndPassword(dummyPasswordHash, []byte(password))
		return nil, "", ErrInvalidCredentials
	}

	// Compare password
	if err := compareHashAndPassword([]byte(user.Password), []byte(password)); err != nil {
		return nil, "", ErrInvalidCredentials
	}

	// Generate JWT
	token, err := s.generateToken(user.ID, user.Role, user.TokenVersion)
	if err != nil {
		return nil, "", errors.New("failed to generate token")
	}

	return user, token, nil
}

// GetUserByID returns a user by their ID.
func (s *authService) GetUserByID(id uint) (*models.User, error) {
	return s.userRepo.FindByID(id)
}

// UpdateProfile updates user name, email, and optionally password.
// Changing the email or password requires the correct current password.
func (s *authService) UpdateProfile(id uint, name, email, oldPassword, newPassword, timezone string) (*models.User, string, error) {
	user, err := s.userRepo.FindByID(id)
	if err != nil {
		return nil, "", ErrUserNotFound
	}

	if timezone != "" {
		if err := ValidateTimezone(timezone); err != nil {
			return nil, "", err
		}
		user.Timezone = timezone
	}

	changingEmail := email != "" && email != user.Email
	changingPassword := newPassword != ""

	// Changing the login email or password requires the current password.
	if changingEmail || changingPassword {
		if oldPassword == "" {
			if changingEmail {
				return nil, "", fmt.Errorf("%w to change email", ErrCurrentPasswordRequired)
			}
			return nil, "", fmt.Errorf("%w to set a new password", ErrCurrentPasswordRequired)
		}
		if changingPassword {
			if err := ValidatePassword(newPassword); err != nil {
				return nil, "", err
			}
		}
		if err := compareHashAndPassword([]byte(user.Password), []byte(oldPassword)); err != nil {
			return nil, "", ErrCurrentPasswordIncorrect
		}
	}

	if name != "" {
		user.Name = name
	}
	if changingEmail {
		existing, _ := s.userRepo.FindByEmail(email)
		if existing != nil && existing.ID != id {
			return nil, "", ErrEmailTaken
		}
		user.Email = email
	}

	if changingPassword {
		hashed, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
		if err != nil {
			return nil, "", errors.New("failed to hash password")
		}
		user.Password = string(hashed)
	}

	if err := s.userRepo.Update(user); err != nil {
		return nil, "", errors.New("failed to update profile")
	}

	if !changingPassword {
		return user, "", nil
	}
	// Revoke every existing session and issue a token for this one.
	version, err := s.userRepo.IncrementTokenVersion(id)
	if err != nil {
		return nil, "", errors.New("failed to update profile")
	}
	user.TokenVersion = version
	token, err := s.generateToken(user.ID, user.Role, version)
	if err != nil {
		return nil, "", errors.New("failed to generate token")
	}
	return user, token, nil
}

// Logout revokes every token issued to the user so far.
func (s *authService) Logout(userID uint) error {
	_, err := s.userRepo.IncrementTokenVersion(userID)
	return err
}

// generateToken creates a signed JWT with user ID, role and token version claims.
func (s *authService) generateToken(userID uint, role string, tokenVersion int64) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"user_id": userID,
		"role":    role,
		"tv":      tokenVersion,
		"exp":     now.Add(AccessTokenTTL).Unix(),
		"iat":     now.Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.jwtSecret))
}
