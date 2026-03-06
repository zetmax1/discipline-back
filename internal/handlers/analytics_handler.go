package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

// AnalyticsHandler proxies requests to the Python Analytics Microservice.
type AnalyticsHandler struct {
	pythonServiceURL string
	apiKey           string
}

// NewAnalyticsHandler creates a new AnalyticsHandler.
func NewAnalyticsHandler(pythonServiceURL, apiKey string) *AnalyticsHandler {
	// Default to local service if not provided
	if pythonServiceURL == "" {
		pythonServiceURL = "https://discipline-py.up.railway.app/" 	
	}
	return &AnalyticsHandler{
		pythonServiceURL: pythonServiceURL,
		apiKey:           apiKey,
	}
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
	userID := c.MustGet("userID").(uint)
	rng := c.Query("range")
	
	pythonRoute := "weekly"
	if rng == "month" {
		pythonRoute = "monthly"
	}

	// Determine Python endpoint
	endpoint := fmt.Sprintf("%s/analytics/%s?user_id=%d", h.pythonServiceURL, pythonRoute, userID)

	// Fetch from Python service
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create analytics request"})
		return
	}
	// Attach security key
	req.Header.Add("X-API-Key", h.apiKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to connect to analytics service"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Analytics service returned an error"})
		return
	}

	// Read and proxy the response directly
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read analytics response"})
		return
	}

	// Unmarshal JSON to avoid returning raw string to Gin
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid JSON from analytics service"})
		return
	}

	c.JSON(http.StatusOK, result)
}

// GetFocusPatterns proxies the focus-patterns endpoint
func (h *AnalyticsHandler) GetFocusPatterns(c *gin.Context) {
	userID := c.MustGet("userID").(uint)
	days := c.Query("days")
	if days == "" {
		days = "30"
	}

	endpoint := fmt.Sprintf("%s/analytics/focus-patterns?user_id=%d&days=%s", h.pythonServiceURL, userID, days)

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create analytics request"})
		return
	}
	req.Header.Add("X-API-Key", h.apiKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to connect to analytics service"})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read analytics response"})
		return
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid JSON from analytics service"})
		return
	}

	c.JSON(http.StatusOK, result)
}
