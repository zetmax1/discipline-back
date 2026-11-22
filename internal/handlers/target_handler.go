package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/apierr"
	"discipline-backend/internal/repositories"
	"discipline-backend/internal/services"
)

// TargetHandler serves GET/PUT /weekly-targets (F9a S3).
type TargetHandler struct{ svc services.TargetService }

func NewTargetHandler(svc services.TargetService) *TargetHandler { return &TargetHandler{svc: svc} }

// TargetsRequest is the body of PUT /weekly-targets.
type TargetsRequest struct {
	Categories        []repositories.CategoryMinutes `json:"categories"`
	MaxFocusMinPerDay *int                           `json:"maxFocusMinPerDay"`
	RestDay           *int                           `json:"restDay"`
}

func respondTargetError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, services.ErrInvalidTargets):
		code, _ := apierr.CodeOf(err)
		apierr.JSON(c, http.StatusBadRequest, code, err.Error())
	case errors.Is(err, repositories.ErrTargetCategory):
		apierr.JSON(c, http.StatusBadRequest, apierr.TargetCategoryInvalid, "categoryId is not one of your categories")
	default:
		apierr.JSON(c, http.StatusInternalServerError, apierr.InternalError, "Failed to save weekly targets")
	}
}

// Get godoc
// @Summary  Weekly targets (F9a); the page defaults with isDefault until the user saves
// @Tags     Commitments
// @Produce  json
// @Security BearerAuth
// @Success  200 {object} services.WeeklyTargets
// @Router   /weekly-targets [get]
func (h *TargetHandler) Get(c *gin.Context) {
	t, err := h.svc.Get(c.MustGet("userID").(uint))
	if err != nil {
		respondTargetError(c, err)
		return
	}
	c.JSON(http.StatusOK, t)
}

// Put godoc
// @Summary  Replace the weekly targets (F9a)
// @Tags     Commitments
// @Accept   json
// @Produce  json
// @Security BearerAuth
// @Param    body body TargetsRequest true "Targets"
// @Success  200 {object} services.WeeklyTargets
// @Failure  400 {object} ErrorResponse
// @Router   /weekly-targets [put]
func (h *TargetHandler) Put(c *gin.Context) {
	var req TargetsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.JSON(c, http.StatusBadRequest, apierr.TargetsInvalid, "Validation failed")
		return
	}
	t, err := h.svc.Save(c.MustGet("userID").(uint), services.WeeklyTargets{Categories: req.Categories, MaxFocusMinPerDay: req.MaxFocusMinPerDay, RestDay: req.RestDay})
	if err != nil {
		respondTargetError(c, err)
		return
	}
	c.JSON(http.StatusOK, t)
}

// RegisterTargetRoutes adds the weekly-target routes (FEATURE_F9A).
func RegisterTargetRoutes(r gin.IRoutes, h *TargetHandler) {
	r.GET("/weekly-targets", h.Get)
	r.PUT("/weekly-targets", h.Put)
}

// LocalImportHandler serves POST /commitments/local-import (F9a S2).
type LocalImportHandler struct{ svc services.LocalImportService }

func NewLocalImportHandler(svc services.LocalImportService) *LocalImportHandler {
	return &LocalImportHandler{svc: svc}
}

// LocalImportRequest is the browser's localStorage data, sent once.
type LocalImportRequest struct {
	Commitments []services.LegacyCommitment `json:"commitments" binding:"max=50"`
	Goals       *services.LegacyGoals       `json:"goals"`
}

// Import godoc
// @Summary  Move the browser's Smart Schedule commitments and goals to the account, once (F9a)
// @Tags     Commitments
// @Accept   json
// @Produce  json
// @Security BearerAuth
// @Param    body body LocalImportRequest true "localStorage data"
// @Success  200 {object} services.LocalImportResult
// @Failure  400 {object} ErrorResponse
// @Router   /commitments/local-import [post]
func (h *LocalImportHandler) Import(c *gin.Context) {
	var req LocalImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.JSON(c, http.StatusBadRequest, apierr.ValidationFailed, "Validation failed")
		return
	}
	res, err := h.svc.Import(c.MustGet("userID").(uint), req.Commitments, req.Goals, userToday(c))
	if err != nil {
		apierr.JSON(c, http.StatusInternalServerError, apierr.InternalError, "Failed to import")
		return
	}
	c.JSON(http.StatusOK, res)
}

// RegisterLocalImportRoute adds POST /commitments/local-import (FEATURE_F9A).
func RegisterLocalImportRoute(r gin.IRoutes, h *LocalImportHandler) {
	r.POST("/commitments/local-import", h.Import)
}
