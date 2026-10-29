package services

import (
	"crypto/rand"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"discipline-backend/internal/models"
	"discipline-backend/internal/repositories"
)

// ErrInvalidCredentials is returned by Login for an unknown email or wrong password.
var ErrInvalidCredentials = errors.New("invalid email or password")

// ErrEmailTaken is returned when registering with an email that already exists.
var ErrEmailTaken = errors.New("user with this email already exists")

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
		return fmt.Errorf("%w: password must be at least %d characters", ErrPasswordPolicy, minPasswordChars)
	}
	if len(password) > maxPasswordBytes {
		return fmt.Errorf("%w: password must be at most %d bytes", ErrPasswordPolicy, maxPasswordBytes)
	}
	return nil
}

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
	UpdateProfile(id uint, name, email, oldPassword, newPassword string) (*models.User, error)
}

type authService struct {
	userRepo  repositories.UserRepository
	jwtSecret string
	jwtExpiry int // hours
}

// NewAuthService creates a new AuthService.
func NewAuthService(userRepo repositories.UserRepository, jwtSecret string, jwtExpiry int) AuthService {
	return &authService{
		userRepo:  userRepo,
		jwtSecret: jwtSecret,
		jwtExpiry: jwtExpiry,
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
	token, err := s.generateToken(user.ID, user.Role)
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
	token, err := s.generateToken(user.ID, user.Role)
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
func (s *authService) UpdateProfile(id uint, name, email, oldPassword, newPassword string) (*models.User, error) {
	user, err := s.userRepo.FindByID(id)
	if err != nil {
		return nil, errors.New("user not found")
	}

	if name != "" {
		user.Name = name
	}
	if email != "" && email != user.Email {
		existing, _ := s.userRepo.FindByEmail(email)
		if existing != nil && existing.ID != id {
			return nil, errors.New("email already in use")
		}
		user.Email = email
	}

	if newPassword != "" {
		if oldPassword == "" {
			return nil, errors.New("current password is required to set a new password")
		}
		if err := ValidatePassword(newPassword); err != nil {
			return nil, err
		}
		if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(oldPassword)); err != nil {
			return nil, errors.New("current password is incorrect")
		}
		hashed, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
		if err != nil {
			return nil, errors.New("failed to hash password")
		}
		user.Password = string(hashed)
	}

	if err := s.userRepo.Update(user); err != nil {
		return nil, errors.New("failed to update profile")
	}

	return user, nil
}

// generateToken creates a signed JWT with user ID and role claims.
func (s *authService) generateToken(userID uint, role string) (string, error) {
	claims := jwt.MapClaims{
		"user_id": userID,
		"role":    role,
		"exp":     time.Now().Add(time.Duration(s.jwtExpiry) * time.Hour).Unix(),
		"iat":     time.Now().Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.jwtSecret))
}
