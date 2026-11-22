package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"discipline-backend/internal/apierr"
	"discipline-backend/internal/models"
	"discipline-backend/internal/repositories"
	"discipline-backend/internal/services"
)

// CategoryHandler serves a user's categories (F13).
type CategoryHandler struct{ svc services.CategoryService }

// NewCategoryHandler creates a CategoryHandler.
func NewCategoryHandler(svc services.CategoryService) *CategoryHandler {
	return &CategoryHandler{svc: svc}
}

// CategoryRequest creates (every field) or updates (any field but kind) a category.
type CategoryRequest struct {
	Name  *string `json:"name,omitempty" example:"Reading"`
	Color *string `json:"color,omitempty" example:"#8B5CF6"` // #RRGGBB
	Icon  *string `json:"icon,omitempty" example:"book-open"`
	Kind  *string `json:"kind,omitempty" example:"study"` // focus|study|admin|exercise|rest|personal; create only
}

// CategoryListResponse lists the user's categories, defaults first.
type CategoryListResponse struct {
	Categories []models.Category `json:"categories"`
}

// CategoryResponse wraps one category.
type CategoryResponse struct {
	Category models.Category `json:"category"`
}

func respondCategoryError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, services.ErrInvalidCategory):
		code, _ := apierr.CodeOf(err)
		apierr.JSON(c, http.StatusBadRequest, code, err.Error())
	case errors.Is(err, repositories.ErrCategoryNameTaken):
		apierr.JSON(c, http.StatusConflict, apierr.CategoryNameTaken, err.Error())
	case errors.Is(err, repositories.ErrCategoryInUse):
		apierr.JSON(c, http.StatusConflict, apierr.CategoryInUse, err.Error())
	case errors.Is(err, repositories.ErrCategoryInvalid):
		apierr.JSON(c, http.StatusBadRequest, apierr.CategoryInvalid, err.Error())
	case errors.Is(err, gorm.ErrRecordNotFound):
		apierr.JSON(c, http.StatusNotFound, apierr.CategoryNotFound, "Category not found")
	default:
		apierr.JSON(c, http.StatusInternalServerError, apierr.InternalError, "Failed to save category")
	}
}

func categoryID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || id == 0 {
		apierr.JSON(c, http.StatusBadRequest, apierr.CategoryNotFound, "Invalid category ID")
		return 0, false
	}
	return uint(id), true
}

// List godoc
// @Summary      List categories
// @Description  The user's categories; the six defaults are created the first time they are read.
// @Tags         Categories
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  CategoryListResponse
// @Router       /categories [get]
func (h *CategoryHandler) List(c *gin.Context) {
	cats, err := h.svc.List(c.MustGet("userID").(uint))
	if err != nil {
		respondCategoryError(c, err)
		return
	}
	c.JSON(http.StatusOK, CategoryListResponse{Categories: cats})
}

// Create godoc
// @Summary      Create a category
// @Tags         Categories
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        Idempotency-Key  header    string           false  "8-100 characters; a replay returns the first category"
// @Param        body             body      CategoryRequest  true   "name, color, icon, kind"
// @Success      201   {object}  CategoryResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse
// @Router       /categories [post]
func (h *CategoryHandler) Create(c *gin.Context) {
	key, ok := optionalIdempotencyKey(c)
	if !ok {
		return
	}
	var req CategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.JSON(c, http.StatusBadRequest, apierr.ValidationFailed, "Validation failed")
		return
	}
	cat, err := h.svc.Create(c.MustGet("userID").(uint), services.CategoryInput(req), key)
	if err != nil {
		respondCategoryError(c, err)
		return
	}
	c.JSON(http.StatusCreated, CategoryResponse{Category: *cat})
}

// Update godoc
// @Summary      Update a category
// @Description  Name, colour and icon; the kind never changes. A new name marks a default as renamed.
// @Tags         Categories
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      int              true  "Category ID"
// @Param        body  body      CategoryRequest  true  "Fields to change"
// @Success      200   {object}  CategoryResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse
// @Router       /categories/{id} [put]
func (h *CategoryHandler) Update(c *gin.Context) {
	id, ok := categoryID(c)
	if !ok {
		return
	}
	var req CategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.JSON(c, http.StatusBadRequest, apierr.ValidationFailed, "Validation failed")
		return
	}
	cat, err := h.svc.Update(id, c.MustGet("userID").(uint), services.CategoryInput(req))
	if err != nil {
		respondCategoryError(c, err)
		return
	}
	c.JSON(http.StatusOK, CategoryResponse{Category: *cat})
}

// Delete godoc
// @Summary      Delete a category
// @Description  A category in use answers 409 unless reassignTo names another of the user's categories; blocks and rules then move there in one transaction. Defaults cannot be deleted.
// @Tags         Categories
// @Security     BearerAuth
// @Param        id          path   int  true   "Category ID"
// @Param        reassignTo  query  int  false  "Category that takes over its blocks and rules"
// @Success      204
// @Failure      400  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Failure      409  {object}  ErrorResponse
// @Router       /categories/{id} [delete]
func (h *CategoryHandler) Delete(c *gin.Context) {
	id, ok := categoryID(c)
	if !ok {
		return
	}
	var reassign *uint
	if raw, set := c.GetQuery("reassignTo"); set {
		v, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			apierr.JSON(c, http.StatusBadRequest, apierr.CategoryInvalid, "reassignTo must be a category id")
			return
		}
		u := uint(v)
		reassign = &u
	}
	if err := h.svc.Delete(id, c.MustGet("userID").(uint), reassign); err != nil {
		respondCategoryError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// RegisterCategoryRoutes adds the category endpoints (FEATURE_F13).
func RegisterCategoryRoutes(r gin.IRoutes, h *CategoryHandler) {
	r.GET("/categories", h.List)
	r.POST("/categories", h.Create)
	r.PUT("/categories/:id", h.Update)
	r.DELETE("/categories/:id", h.Delete)
}

// PlannerHandlers groups the planner handlers: F13's (FEATURE_F13) and F9a's (FEATURE_F9A).
type PlannerHandlers struct {
	Categories  *CategoryHandler
	Recurring   *RecurringHandler
	Commitments *CommitmentHandler // F9a
	Targets     *TargetHandler
	LocalImport *LocalImportHandler
	// ScheduleInputs builds Smart Schedule's input from the server's data (F9a S5).
	ScheduleInputs services.ScheduleInputs
	Calendar       *CalendarHandler
}
