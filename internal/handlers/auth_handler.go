package handlers

import (
	"errors"
	"log"
	"math"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/models"
	"discipline-backend/internal/services"
)

// AuthHandler handles authentication HTTP requests.
type AuthHandler struct {
	authService   services.AuthService
	loginThrottle *services.LoginThrottle
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(authService services.AuthService, loginThrottle *services.LoginThrottle) *AuthHandler {
	return &AuthHandler{authService: authService, loginThrottle: loginThrottle}
}

// --- Request / Response types ---

// RegisterRequest is the expected JSON body for registration.
type RegisterRequest struct {
	Name     string `json:"name" binding:"required,min=2,max=100" example:"John Doe"`
	Email    string `json:"email" binding:"required,email" example:"john@example.com"`
	Password string `json:"password" binding:"required" example:"securepass123"` // policy: 10+ chars, max 72 bytes
}

// LoginRequest is the expected JSON body for login.
type LoginRequest struct {
	Email    string `json:"email" binding:"required,email" example:"john@example.com"`
	Password string `json:"password" binding:"required" example:"securepass123"`
}

// AuthResponse is the response for successful auth operations.
type AuthResponse struct {
	Message string       `json:"message" example:"Login successful"`
	User    *models.User `json:"user"`
	Token   string       `json:"token" example:"eyJhbGciOiJIUzI1NiIs..."`
}

// ErrorResponse is the standard error response.
type ErrorResponse struct {
	Error   string `json:"error" example:"Validation failed"`
	Details string `json:"details,omitempty" example:"Key: 'Email' required"`
}

// UserResponse wraps user info for the /me endpoint.
type UserResponse struct {
	User *models.User `json:"user"`
}

// respondRegisterError maps registration errors: policy violations are 400,
// an existing email stays 409, anything else is 500.
func respondRegisterError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, services.ErrPasswordPolicy):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, services.ErrEmailTaken):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Registration failed"})
	}
}

// --- Handlers ---

// Register godoc
// @Summary      Register a new user
// @Description  Creates a new user account with role "user" and returns a JWT token
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        body  body      RegisterRequest  true  "Registration data"
// @Success      201   {object}  AuthResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse
// @Router       /auth/register [post]
func (h *AuthHandler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Validation failed",
			"details": err.Error(),
		})
		return
	}

	user, token, err := h.authService.Register(req.Name, req.Email, req.Password)
	if err != nil {
		respondRegisterError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "User registered successfully",
		"user":    user,
		"token":   token,
	})
}

// Login godoc
// @Summary      Login
// @Description  Authenticates a user and returns a JWT token
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        body  body      LoginRequest  true  "Login credentials"
// @Success      200   {object}  AuthResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      429   {object}  ErrorResponse
// @Failure      503   {object}  ErrorResponse
// @Router       /auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Validation failed",
			"details": err.Error(),
		})
		return
	}

	ctx := c.Request.Context()
	locked, retryAfter, err := h.loginThrottle.Locked(ctx, req.Email)
	if err != nil {
		// Fail closed: without the shared failure count there is no brute-force protection.
		log.Printf("login throttle unavailable: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Service temporarily unavailable. Please try again shortly."})
		return
	}
	if locked {
		c.Header("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": "Too many failed login attempts. Please try again later.",
		})
		return
	}

	user, token, err := h.authService.Login(req.Email, req.Password)
	if err != nil {
		if errors.Is(err, services.ErrInvalidCredentials) {
			if err := h.loginThrottle.RecordFailure(ctx, req.Email); err != nil {
				log.Printf("login throttle: record failure: %v", err)
			}
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": err.Error(),
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Login failed",
		})
		return
	}
	if err := h.loginThrottle.Reset(ctx, req.Email); err != nil {
		log.Printf("login throttle: reset: %v", err)
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Login successful",
		"user":    user,
		"token":   token,
	})
}

// Me godoc
// @Summary      Get current user
// @Description  Returns the authenticated user's profile
// @Tags         Auth
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  UserResponse
// @Failure      401  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /me [get]
func (h *AuthHandler) Me(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "User not found in context",
		})
		return
	}

	user, err := h.authService.GetUserByID(userID.(uint))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "User not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user": user,
	})
}

// RegisterAdmin godoc
// @Summary      Register a new admin (admin only)
// @Description  Creates a new user account with role "admin". Only accessible by admins.
// @Tags         Admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      RegisterRequest  true  "Admin registration data"
// @Success      201   {object}  AuthResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      403   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse
// @Router       /admin/register [post]
func (h *AuthHandler) RegisterAdmin(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Validation failed",
			"details": err.Error(),
		})
		return
	}

	user, token, err := h.authService.RegisterAdmin(req.Name, req.Email, req.Password)
	if err != nil {
		respondRegisterError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "Admin registered successfully",
		"user":    user,
		"token":   token,
	})
}

// UpdateProfileRequest is the expected JSON body for profile updates.
type UpdateProfileRequest struct {
	Name        string `json:"name,omitempty" binding:"omitempty,max=100" example:"Jane Doe"`
	Email       string `json:"email,omitempty" binding:"omitempty,email" example:"jane@example.com"`
	OldPassword string `json:"oldPassword,omitempty" example:"oldpass123"`                            // required to change email or password
	Timezone    string `json:"timezone,omitempty" binding:"omitempty,max=64" example:"Asia/Tashkent"` // IANA name
	NewPassword string `json:"newPassword,omitempty" example:"newpass456"`
}

// UpdateProfile godoc
// @Summary      Update user profile
// @Description  Updates the authenticated user's name, email, and/or password. Changing email or password requires oldPassword.
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      UpdateProfileRequest  true  "Profile update data"
// @Success      200   {object}  UserResponse
// @Failure      400   {object}  ErrorResponse
// @Router       /me [put]
func (h *AuthHandler) UpdateProfile(c *gin.Context) {
	userID := c.MustGet("userID").(uint)

	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Validation failed",
			"details": err.Error(),
		})
		return
	}

	user, token, err := h.authService.UpdateProfile(userID, req.Name, req.Email, req.OldPassword, req.NewPassword, req.Timezone)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": err.Error(),
		})
		return
	}

	resp := gin.H{"user": user}
	if token != "" {
		// The password changed: all older tokens are revoked, this one replaces them.
		resp["token"] = token
	}
	c.JSON(http.StatusOK, resp)
}

// Logout godoc
// @Summary      Log out everywhere
// @Description  Revokes every token issued to the authenticated user
// @Tags         Auth
// @Security     BearerAuth
// @Success      204
// @Failure      401  {object}  ErrorResponse
// @Router       /auth/logout [post]
func (h *AuthHandler) Logout(c *gin.Context) {
	if err := h.authService.Logout(c.MustGet("userID").(uint)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Logout failed"})
		return
	}
	c.Status(http.StatusNoContent)
}
