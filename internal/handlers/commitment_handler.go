package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/apierr"
	"discipline-backend/internal/repositories"
	"discipline-backend/internal/services"
)

// CommitmentHandler serves the F9a commitment routes (FEATURE_F9A).
type CommitmentHandler struct{ svc services.CommitmentService }

func NewCommitmentHandler(svc services.CommitmentService) *CommitmentHandler {
	return &CommitmentHandler{svc: svc}
}

// CommitmentRequest is the body of POST and PUT /commitments(/:id).
type CommitmentRequest struct {
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	Weekdays  []int   `json:"weekdays"`
	StartMin  *int    `json:"startMin"`
	EndMin    *int    `json:"endMin"`
	ValidFrom string  `json:"validFrom"`
	ValidTo   *string `json:"validTo"`
}

func (r CommitmentRequest) input() services.CommitmentInput {
	in := services.CommitmentInput{Name: r.Name, Kind: r.Kind, Weekdays: r.Weekdays, ValidFrom: r.ValidFrom, ValidTo: r.ValidTo, StartMin: -1, EndMin: -1}
	if r.StartMin != nil {
		in.StartMin = *r.StartMin
	}
	if r.EndMin != nil {
		in.EndMin = *r.EndMin
	}
	return in
}

func respondCommitmentError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, services.ErrInvalidCommitment):
		code, ok := apierr.CodeOf(err)
		if !ok {
			code = apierr.ValidationFailed
		}
		apierr.JSON(c, http.StatusBadRequest, code, err.Error())
	case errors.Is(err, repositories.ErrCommitmentNotFound):
		apierr.JSON(c, http.StatusNotFound, apierr.CommitmentNotFound, "Commitment not found")
	case errors.Is(err, repositories.ErrCommitmentLimit):
		apierr.JSON(c, http.StatusBadRequest, apierr.CommitmentLimitReached, "At most 100 commitments")
	default:
		apierr.JSON(c, http.StatusInternalServerError, apierr.InternalError, "Failed to save commitment")
	}
}

// List godoc
// @Summary  List commitments (F9a)
// @Tags     Commitments
// @Produce  json
// @Security BearerAuth
// @Success  200 {object} map[string]interface{}
// @Router   /commitments [get]
func (h *CommitmentHandler) List(c *gin.Context) {
	list, err := h.svc.List(c.MustGet("userID").(uint))
	if err != nil {
		respondCommitmentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"commitments": list})
}

// Create godoc
// @Summary  Create a commitment (F9a); an optional Idempotency-Key makes a replay return the first one
// @Tags     Commitments
// @Accept   json
// @Produce  json
// @Security BearerAuth
// @Param    body body CommitmentRequest true "The commitment"
// @Success  201 {object} map[string]interface{}
// @Failure  400 {object} ErrorResponse
// @Router   /commitments [post]
func (h *CommitmentHandler) Create(c *gin.Context) {
	key, ok := optionalIdempotencyKey(c)
	if !ok {
		return
	}
	var req CommitmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.JSON(c, http.StatusBadRequest, apierr.ValidationFailed, "Validation failed")
		return
	}
	out, err := h.svc.Create(c.MustGet("userID").(uint), req.input(), key)
	if err != nil {
		respondCommitmentError(c, err)
		return
	}
	c.JSON(http.StatusCreated, out)
}

// Update godoc
// @Summary  Edit a commitment (F9a); an imported one keeps its source
// @Tags     Commitments
// @Accept   json
// @Produce  json
// @Security BearerAuth
// @Param    id   path int               true "Commitment ID"
// @Param    body body CommitmentRequest true "All fields"
// @Success  200 {object} map[string]interface{}
// @Failure  404 {object} ErrorResponse
// @Router   /commitments/{id} [put]
func (h *CommitmentHandler) Update(c *gin.Context) {
	id, ok := pathID(c, apierr.CommitmentNotFound)
	if !ok {
		return
	}
	var req CommitmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.JSON(c, http.StatusBadRequest, apierr.ValidationFailed, "Validation failed")
		return
	}
	out, err := h.svc.Update(id, c.MustGet("userID").(uint), req.input())
	if err != nil {
		respondCommitmentError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// Delete godoc
// @Summary  Delete a commitment (F9a)
// @Tags     Commitments
// @Security BearerAuth
// @Param    id path int true "Commitment ID"
// @Success  204
// @Failure  404 {object} ErrorResponse
// @Router   /commitments/{id} [delete]
func (h *CommitmentHandler) Delete(c *gin.Context) {
	id, ok := pathID(c, apierr.CommitmentNotFound)
	if !ok {
		return
	}
	if err := h.svc.Delete(id, c.MustGet("userID").(uint)); err != nil {
		respondCommitmentError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Occurrences godoc
// @Summary  Commitment segments in a range of at most 42 days (F9a); overnight ones split at midnight
// @Tags     Commitments
// @Produce  json
// @Security BearerAuth
// @Param    startDate query string true "YYYY-MM-DD"
// @Param    endDate   query string true "YYYY-MM-DD"
// @Success  200 {object} map[string]interface{}
// @Failure  400 {object} ErrorResponse
// @Router   /commitments/occurrences [get]
func (h *CommitmentHandler) Occurrences(c *gin.Context) {
	segs, err := h.svc.Occurrences(c.MustGet("userID").(uint), c.Query("startDate"), c.Query("endDate"))
	if err != nil {
		respondCommitmentError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"segments": segs})
}

// RegisterCommitmentRoutes adds the commitment routes (FEATURE_F9A).
func RegisterCommitmentRoutes(r gin.IRoutes, h *CommitmentHandler) {
	r.GET("/commitments", h.List)
	r.POST("/commitments", h.Create)
	r.GET("/commitments/occurrences", h.Occurrences)
	r.PUT("/commitments/:id", h.Update)
	r.DELETE("/commitments/:id", h.Delete)
}
