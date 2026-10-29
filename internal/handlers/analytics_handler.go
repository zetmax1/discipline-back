package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/golang-jwt/jwt/v5"
)

// Internal token settings shared with the Python service.
const (
	internalTokenAudience = "discipline-py"
	internalTokenTTL      = 60 * time.Second
)

// AnalyticsClientTimeout bounds every call to the Python service.
const AnalyticsClientTimeout = 10 * time.Second

// maxAnalyticsResponseBytes caps upstream responses (they embed base64 chart images).
const maxAnalyticsResponseBytes = 10 << 20

// analyticsClient is shared by all analytics handlers so connections are reused.
var analyticsClient = &http.Client{Timeout: AnalyticsClientTimeout}

// AnalyticsHandler proxies requests to the Python Analytics Microservice.
type AnalyticsHandler struct {
	pythonServiceURL string
	internalSecret   []byte
	client           *http.Client
	maxResponseBytes int64
}

// NewAnalyticsHandler creates a new AnalyticsHandler. internalSecret signs the
// per-request token that tells the Python service which user the request is for.
func NewAnalyticsHandler(pythonServiceURL, internalSecret string) *AnalyticsHandler {
	// Remove trailing slashes to avoid double-slash URLs (e.g. //analytics/weekly)
	pythonServiceURL = strings.TrimRight(pythonServiceURL, "/")
	return &AnalyticsHandler{
		pythonServiceURL: pythonServiceURL,
		internalSecret:   []byte(internalSecret),
		client:           analyticsClient,
		maxResponseBytes: maxAnalyticsResponseBytes,
	}
}

// internalClaims identify the user and their IANA time zone to the Python service.
type internalClaims struct {
	TZ string `json:"tz"`
	jwt.RegisteredClaims
}

// internalToken signs a short-lived HS256 token identifying the user to the Python service.
func (h *AnalyticsHandler) internalToken(userID uint, tz string) (string, error) {
	now := time.Now()
	claims := internalClaims{
		TZ: tz,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatUint(uint64(userID), 10),
			Audience:  jwt.ClaimStrings{internalTokenAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(internalTokenTTL)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(h.internalSecret)
}

const (
	defaultFocusDays = 30
	minFocusDays     = 7
	maxFocusDays     = 365
)

// SmartScheduleCommitment is a fixed commitment sent to the schedule generator.
type SmartScheduleCommitment struct {
	ID        string   `json:"id" binding:"required,max=64"`
	Name      string   `json:"name" binding:"required,min=1,max=100"`
	StartTime string   `json:"startTime" binding:"required"`
	EndTime   string   `json:"endTime" binding:"required"`
	Days      []string `json:"days" binding:"required,min=1,max=7,dive,oneof=Monday Tuesday Wednesday Thursday Friday Saturday Sunday"`
}

// SmartScheduleGoals are weekly goal hours; omitted fields fall back to the generator's defaults.
type SmartScheduleGoals struct {
	DeepWorkHours *float64 `json:"deepWorkHours,omitempty" binding:"omitempty,min=0,max=168"`
	StudyHours    *float64 `json:"studyHours,omitempty" binding:"omitempty,min=0,max=168"`
	RestHours     *float64 `json:"restHours,omitempty" binding:"omitempty,min=0,max=168"`
}

// SmartScheduleRequest is the only body shape forwarded to the Python service.
type SmartScheduleRequest struct {
	Commitments []SmartScheduleCommitment `json:"commitments" binding:"max=50,dive"`
	Goals       *SmartScheduleGoals       `json:"goals" binding:"required"`
}

var hhmm = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// decodeSmartSchedule strictly decodes and validates the request body.
func decodeSmartSchedule(body io.Reader) (*SmartScheduleRequest, error) {
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	var req SmartScheduleRequest
	if err := dec.Decode(&req); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("unexpected data after JSON body")
	}
	if err := binding.Validator.ValidateStruct(&req); err != nil {
		return nil, err
	}
	for i, c := range req.Commitments {
		if !hhmm.MatchString(c.StartTime) || !hhmm.MatchString(c.EndTime) {
			return nil, fmt.Errorf("commitments[%d]: times must be HH:MM between 00:00 and 23:59", i)
		}
	}
	if req.Commitments == nil {
		req.Commitments = []SmartScheduleCommitment{}
	}
	return &req, nil
}

// upstreamURL builds a Python service URL from a fixed path and server-built query values.
func (h *AnalyticsHandler) upstreamURL(path string, query url.Values) string {
	if len(query) == 0 {
		return h.pythonServiceURL + path
	}
	return h.pythonServiceURL + path + "?" + query.Encode()
}

// forward sends the request to the Python service on behalf of the authenticated
// user and relays its JSON response.
func (h *AnalyticsHandler) forward(c *gin.Context, method, endpoint string, body []byte) {
	token, err := h.internalToken(c.MustGet("userID").(uint), userLocation(c).String())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create analytics request"})
		return
	}

	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), method, endpoint, rd)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create analytics request"})
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Add("Content-Type", "application/json")
	}

	resp, err := h.client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to connect to analytics service"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Analytics service returned an error"})
		return
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, h.maxResponseBytes+1))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to read analytics response"})
		return
	}
	if int64(len(respBody)) > h.maxResponseBytes {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Analytics response too large"})
		return
	}

	// Unmarshal JSON to avoid returning raw string to Gin
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid JSON from analytics service"})
		return
	}

	c.JSON(http.StatusOK, result)
}

// GetAnalytics godoc
// @Summary      Get productivity analytics
// @Description  Proxies request to the Python analytics microservice securely using the authenticated user's ID.
// @Tags         Analytics
// @Produce      json
// @Security     BearerAuth
// @Param        range  query  string  false  "Report range (week or month, defaults to week)"  example(week)
// @Success      200    {object}  map[string]interface{}
// @Failure      400    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /analytics [get]
func (h *AnalyticsHandler) GetAnalytics(c *gin.Context) {
	var pythonRoute string
	switch c.DefaultQuery("range", "week") {
	case "week":
		pythonRoute = "/analytics/weekly"
	case "month":
		pythonRoute = "/analytics/monthly"
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "range must be 'week' or 'month'"})
		return
	}

	h.forward(c, http.MethodGet, h.upstreamURL(pythonRoute, nil), nil)
}

// GetToday godoc
// @Summary      Today's dashboard metrics
// @Description  Planned/done minutes and focus-score components for today in the user's time zone, computed by the analytics service.
// @Tags         Dashboard
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      502  {object}  ErrorResponse
// @Router       /dashboard/today [get]
func (h *AnalyticsHandler) GetToday(c *gin.Context) {
	h.forward(c, http.MethodGet, h.upstreamURL("/analytics/today", nil), nil)
}

// GetFocusPatterns proxies the focus-patterns endpoint
func (h *AnalyticsHandler) GetFocusPatterns(c *gin.Context) {
	days := defaultFocusDays
	if raw := c.Query("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < minFocusDays || n > maxFocusDays {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("days must be an integer between %d and %d", minFocusDays, maxFocusDays),
			})
			return
		}
		days = n
	}

	query := url.Values{}
	query.Set("days", strconv.Itoa(days))
	h.forward(c, http.MethodGet, h.upstreamURL("/analytics/focus-patterns", query), nil)
}

// GenerateSmartSchedule proxies the smart-schedule endpoint
// @Summary      Generate smart schedule
// @Description  Proxies request to Python to generate a weekly schedule based on user's past habits.
// @Tags         Analytics
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body  interface{}  true  "Smart schedule request (commitments and goals)"
// @Success      200   {object}  map[string]interface{}
// @Failure      400   {object}  ErrorResponse
// @Failure      500   {object}  ErrorResponse
// @Router       /analytics/smart-schedule [post]
func (h *AnalyticsHandler) GenerateSmartSchedule(c *gin.Context) {
	req, err := decodeSmartSchedule(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Validation failed",
			"details": err.Error(),
		})
		return
	}

	// Re-encode the validated struct; the client's raw body is never forwarded.
	body, err := json.Marshal(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create request"})
		return
	}

	h.forward(c, http.MethodPost, h.upstreamURL("/analytics/smart-schedule", nil), body)
}
