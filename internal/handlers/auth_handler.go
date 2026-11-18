package handlers

import (
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/apierr"
	"discipline-backend/internal/captcha"
	"discipline-backend/internal/models"
	"discipline-backend/internal/services"
)

// AuthHandler handles authentication HTTP requests.
type AuthHandler struct {
	authService   services.AuthService
	loginThrottle *services.LoginThrottle
	captcha       captcha.Verifier
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(authService services.AuthService, loginThrottle *services.LoginThrottle, verifier captcha.Verifier) *AuthHandler {
	return &AuthHandler{authService: authService, loginThrottle: loginThrottle, captcha: verifier}
}

// --- Request / Response types ---

// RegisterRequest is the expected JSON body for registration.
type RegisterRequest struct {
	Name     string `json:"name" binding:"required,min=2,max=100" example:"John Doe"`
	Email    string `json:"email" binding:"required,email" example:"john@example.com"`
	Password string `json:"password" binding:"required" example:"securepass123"` // policy: 10+ chars, max 72 bytes
	// TurnstileToken is required on public registration (not on admin registration).
	TurnstileToken string `json:"turnstileToken,omitempty" binding:"max=2048" example:"0.turnstile-token"`
}

// LoginRequest is the expected JSON body for login.
type LoginRequest struct {
	Email    string `json:"email" binding:"required,email" example:"john@example.com"`
	Password string `json:"password" binding:"required" example:"securepass123"`
	// TurnstileToken is required once the email has 3 failed logins in 15 minutes.
	TurnstileToken string `json:"turnstileToken,omitempty" binding:"max=2048" example:"0.turnstile-token"`
}

// LoginErrorResponse is a 401 from login; captchaRequired tells the client to
// send a Turnstile token with the next attempt.
type LoginErrorResponse struct {
	Error           string `json:"error" example:"invalid email or password"`
	Code            string `json:"code" example:"invalid_credentials"`
	CaptchaRequired bool   `json:"captchaRequired" example:"false"`
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
	Code    string `json:"code" example:"validation_failed"` // stable; the web translates it (contracts/errors.schema.json)
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
		code, _ := apierr.CodeOf(err)
		apierr.JSON(c, http.StatusBadRequest, code, err.Error())
	case errors.Is(err, services.ErrEmailTaken):
		apierr.JSON(c, http.StatusConflict, apierr.EmailTaken, err.Error())
	default:
		apierr.JSON(c, http.StatusInternalServerError, apierr.InternalError, "Registration failed")
	}
}

// respondUnavailable answers 503 when a protection (throttle store, captcha check) cannot run.
// respondProfileError sends a coded profile error as 400 (404 for a missing user) and anything else as 500.
func respondProfileError(c *gin.Context, err error) {
	code, ok := apierr.CodeOf(err)
	switch {
	case !ok:
		apierr.JSON(c, http.StatusInternalServerError, apierr.InternalError, "Failed to update profile")
	case code == apierr.UserNotFound:
		apierr.JSON(c, http.StatusNotFound, code, err.Error())
	default:
		apierr.JSON(c, http.StatusBadRequest, code, err.Error())
	}
}

func respondUnavailable(c *gin.Context) {
	apierr.JSON(c, http.StatusServiceUnavailable, apierr.ServiceUnavailable, "Service temporarily unavailable. Please try again shortly.")
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
// @Failure      503   {object}  ErrorResponse
// @Router       /auth/register [post]
func (h *AuthHandler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.JSON(c, http.StatusBadRequest, apierr.ValidationFailed, "Validation failed", gin.H{"details": err.Error()})
		return
	}

	ok, err := h.captcha.Verify(c.Request.Context(), req.TurnstileToken)
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "captcha verification unavailable", "error", err)
		respondUnavailable(c)
		return
	}
	if !ok {
		apierr.JSON(c, http.StatusBadRequest, apierr.CaptchaRequired, "Please complete the verification.", gin.H{"captchaRequired": true})
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
// @Description  Authenticates a user and returns a JWT token. Failures are throttled per (email, IP) with a progressive delay (429 + Retry-After) and per IP; after 3 failures for an email within 15 minutes, a Turnstile token is required (401 with captchaRequired).
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        body  body      LoginRequest  true  "Login credentials"
// @Success      200   {object}  AuthResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  LoginErrorResponse
// @Failure      429   {object}  ErrorResponse
// @Failure      503   {object}  ErrorResponse
// @Router       /auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.JSON(c, http.StatusBadRequest, apierr.ValidationFailed, "Validation failed", gin.H{"details": err.Error()})
		return
	}

	ctx := c.Request.Context()
	ip := c.ClientIP()
	state, err := h.loginThrottle.Check(ctx, req.Email, ip)
	if err != nil {
		// Fail closed: without the shared failure counts there is no brute-force protection.
		slog.ErrorContext(ctx, "login throttle unavailable", "error", err)
		respondUnavailable(c)
		return
	}
	if state.RetryAfter > 0 {
		c.Header("Retry-After", strconv.Itoa(max(1, int(math.Ceil(state.RetryAfter.Seconds())))))
		apierr.JSON(c, http.StatusTooManyRequests, apierr.LoginThrottled, "Too many failed login attempts. Please try again later.")
		return
	}
	if state.CaptchaRequired {
		ok, err := h.captcha.Verify(ctx, req.TurnstileToken)
		if err != nil {
			slog.ErrorContext(ctx, "captcha verification unavailable", "error", err)
			respondUnavailable(c)
			return
		}
		if !ok {
			apierr.JSON(c, http.StatusUnauthorized, apierr.CaptchaRequired, "Please complete the verification.", gin.H{"captchaRequired": true})
			return
		}
	}

	user, token, err := h.authService.Login(req.Email, req.Password)
	if err != nil {
		if errors.Is(err, services.ErrInvalidCredentials) {
			captchaNext, recErr := h.loginThrottle.RecordFailure(ctx, req.Email, ip)
			if recErr != nil {
				slog.ErrorContext(ctx, "login throttle: record failure", "error", recErr)
				captchaNext = state.CaptchaRequired
			}
			apierr.JSON(c, http.StatusUnauthorized, apierr.InvalidCredentials, err.Error(), gin.H{"captchaRequired": captchaNext})
			return
		}
		apierr.JSON(c, http.StatusInternalServerError, apierr.InternalError, "Login failed")
		return
	}
	if err := h.loginThrottle.RecordSuccess(ctx, req.Email, ip); err != nil {
		slog.ErrorContext(ctx, "login throttle: record success", "error", err)
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
		apierr.JSON(c, http.StatusUnauthorized, apierr.AuthRequired, "User not found in context")
		return
	}

	user, err := h.authService.GetUserByID(userID.(uint))
	if err != nil {
		apierr.JSON(c, http.StatusNotFound, apierr.UserNotFound, "User not found")
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
		apierr.JSON(c, http.StatusBadRequest, apierr.ValidationFailed, "Validation failed", gin.H{"details": err.Error()})
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
		apierr.JSON(c, http.StatusBadRequest, apierr.ValidationFailed, "Validation failed", gin.H{"details": err.Error()})
		return
	}

	user, token, err := h.authService.UpdateProfile(userID, req.Name, req.Email, req.OldPassword, req.NewPassword, req.Timezone)
	if err != nil {
		respondProfileError(c, err)
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
		apierr.JSON(c, http.StatusInternalServerError, apierr.InternalError, "Logout failed")
		return
	}
	c.Status(http.StatusNoContent)
}
