package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// FeatureHandler tells signed-in clients which FEATURE_<id> flags are on.
type FeatureHandler struct {
	enabled []string
}

// FeaturesResponse lists the enabled feature ids, e.g. ["F1", "F12A"].
type FeaturesResponse struct {
	Features []string `json:"features"`
}

func NewFeatureHandler(enabled []string) *FeatureHandler {
	if enabled == nil {
		enabled = []string{}
	}
	return &FeatureHandler{enabled: enabled}
}

// List godoc
// @Summary      Enabled feature flags
// @Description  Ids of the features switched on with FEATURE_<id>=true on the server. Anything not listed is off.
// @Tags         Features
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  FeaturesResponse
// @Router       /features [get]
func (h *FeatureHandler) List(c *gin.Context) {
	c.JSON(http.StatusOK, FeaturesResponse{Features: h.enabled})
}
