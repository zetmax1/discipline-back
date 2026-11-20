package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/apierr"
	"discipline-backend/internal/models"
	"discipline-backend/internal/repositories"
	"discipline-backend/internal/services"
)

// RecurringHandler serves recurring rules, their occurrences and the range read (F13).
type RecurringHandler struct{ svc services.RecurringService }

// NewRecurringHandler creates a RecurringHandler.
func NewRecurringHandler(svc services.RecurringService) *RecurringHandler {
	return &RecurringHandler{svc: svc}
}

// RuleRequest creates a rule (every field but validTo) or edits one (any field; "validTo": null
// removes the end date).
type RuleRequest struct {
	CategoryID *uint           `json:"categoryId,omitempty" example:"4"`
	Name       *string         `json:"name,omitempty" example:"Gym"`
	Weekdays   []int           `json:"weekdays,omitempty" example:"1,3,5"` // ISO: Mon = 1 … Sun = 7
	StartMin   *int            `json:"startMin,omitempty" example:"1080"`
	EndMin     *int            `json:"endMin,omitempty" example:"1170"`
	ValidFrom  *string         `json:"validFrom,omitempty" example:"2026-11-02"`
	ValidTo    json.RawMessage `json:"validTo,omitempty" swaggertype:"string" example:"2026-12-31"`
}

// RuleListResponse lists the user's recurring rules.
type RuleListResponse struct {
	Rules []models.RecurringRule `json:"rules"`
}

// RuleResponse wraps one rule.
type RuleResponse struct {
	Rule models.RecurringRule `json:"rule"`
}

func (req RuleRequest) input() (services.RuleInput, bool) {
	in := services.RuleInput{CategoryID: req.CategoryID, Name: req.Name, Weekdays: req.Weekdays,
		StartMin: req.StartMin, EndMin: req.EndMin, ValidFrom: req.ValidFrom}
	switch raw := string(req.ValidTo); raw {
	case "":
	case "null":
		in.ClearValidTo = true
	default:
		var s string
		if err := json.Unmarshal(req.ValidTo, &s); err != nil {
			return in, false
		}
		in.ValidTo = &s
	}
	return in, true
}

func respondRecurringError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, services.ErrInvalidRule), errors.Is(err, services.ErrInvalidTask):
		code, ok := apierr.CodeOf(err)
		if !ok {
			code = apierr.ValidationFailed
		}
		apierr.JSON(c, http.StatusBadRequest, code, err.Error())
	case errors.Is(err, repositories.ErrRuleTimes):
		apierr.JSON(c, http.StatusBadRequest, apierr.TaskEndBeforeStart, err.Error())
	case errors.Is(err, repositories.ErrRuleDates):
		apierr.JSON(c, http.StatusBadRequest, apierr.RecurringDatesInvalid, err.Error())
	case errors.Is(err, repositories.ErrCategoryInvalid):
		apierr.JSON(c, http.StatusBadRequest, apierr.CategoryInvalid, "categoryId is not one of your categories")
	case errors.Is(err, repositories.ErrRuleNotFound):
		apierr.JSON(c, http.StatusNotFound, apierr.RecurringRuleNotFound, "Recurring rule not found")
	case errors.Is(err, repositories.ErrOccurrenceNotFound):
		apierr.JSON(c, http.StatusNotFound, apierr.OccurrenceNotFound, "Occurrence not found")
	case errors.Is(err, repositories.ErrTaskOverlap):
		apierr.JSON(c, http.StatusConflict, apierr.TaskOverlap, err.Error())
	default:
		apierr.JSON(c, http.StatusInternalServerError, apierr.InternalError, "Failed to save recurring block")
	}
}

func pathID(c *gin.Context, code apierr.Code) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || id == 0 {
		apierr.JSON(c, http.StatusNotFound, code, "Invalid ID")
		return 0, false
	}
	return uint(id), true
}

// scopeDate reads ?scope=all|following&fromDate=; "following" needs fromDate.
func scopeDate(c *gin.Context) (string, bool) {
	switch c.DefaultQuery("scope", "all") {
	case "all":
		return "", true
	case "following":
		if d := c.Query("fromDate"); d != "" {
			return d, true
		}
	}
	apierr.JSON(c, http.StatusBadRequest, apierr.RecurringDatesInvalid, "scope must be all, or following with fromDate=YYYY-MM-DD")
	return "", false
}

// ListRules godoc
// @Summary      List recurring rules
// @Tags         Recurring
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  RuleListResponse
// @Router       /recurring-rules [get]
func (h *RecurringHandler) ListRules(c *gin.Context) {
	rules, err := h.svc.ListRules(c.MustGet("userID").(uint))
	if err != nil {
		respondRecurringError(c, err)
		return
	}
	if rules == nil {
		rules = []models.RecurringRule{}
	}
	c.JSON(http.StatusOK, RuleListResponse{Rules: rules})
}

// CreateRule godoc
// @Summary      Create a recurring rule
// @Description  Weekdays are ISO (Mon = 1 … Sun = 7), sorted and de-duplicated. Occurrences appear when a range is read.
// @Tags         Recurring
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        Idempotency-Key  header    string       false  "8-100 characters; a replay returns the first rule"
// @Param        body             body      RuleRequest  true   "The rule"
// @Success      201   {object}  RuleResponse
// @Failure      400   {object}  ErrorResponse
// @Router       /recurring-rules [post]
func (h *RecurringHandler) CreateRule(c *gin.Context) {
	key, ok := optionalIdempotencyKey(c)
	if !ok {
		return
	}
	var req RuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.JSON(c, http.StatusBadRequest, apierr.ValidationFailed, "Validation failed")
		return
	}
	in, ok := req.input()
	if !ok {
		apierr.JSON(c, http.StatusBadRequest, apierr.RecurringDatesInvalid, "validTo must be a date or null")
		return
	}
	rule, err := h.svc.CreateRule(c.MustGet("userID").(uint), in, key)
	if err != nil {
		respondRecurringError(c, err)
		return
	}
	c.JSON(http.StatusCreated, RuleResponse{Rule: *rule})
}

// UpdateRule godoc
// @Summary      Edit a recurring rule
// @Description  scope=all changes the rule and its future untouched occurrences (the past is never rewritten); scope=following&fromDate= splits it, keeping occurrences edited by hand.
// @Tags         Recurring
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id        path      int          true   "Rule ID"
// @Param        scope     query     string       false  "all (default) or following"
// @Param        fromDate  query     string       false  "YYYY-MM-DD, with scope=following"
// @Param        body      body      RuleRequest  true   "Fields to change"
// @Success      200       {object}  RuleResponse
// @Failure      400       {object}  ErrorResponse
// @Failure      404       {object}  ErrorResponse
// @Router       /recurring-rules/{id} [put]
func (h *RecurringHandler) UpdateRule(c *gin.Context) {
	id, ok := pathID(c, apierr.RecurringRuleNotFound)
	if !ok {
		return
	}
	from, ok := scopeDate(c)
	if !ok {
		return
	}
	var req RuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.JSON(c, http.StatusBadRequest, apierr.ValidationFailed, "Validation failed")
		return
	}
	in, ok := req.input()
	if !ok {
		apierr.JSON(c, http.StatusBadRequest, apierr.RecurringDatesInvalid, "validTo must be a date or null")
		return
	}
	rule, err := h.svc.UpdateRule(id, c.MustGet("userID").(uint), in, from, userToday(c))
	if err != nil {
		respondRecurringError(c, err)
		return
	}
	c.JSON(http.StatusOK, RuleResponse{Rule: *rule})
}

// DeleteRule godoc
// @Summary      Delete a recurring rule
// @Description  scope=all removes the rule and its future untouched occurrences; scope=following&fromDate= ends it the day before. Occurrences that happened stay as history.
// @Tags         Recurring
// @Security     BearerAuth
// @Param        id        path   int     true   "Rule ID"
// @Param        scope     query  string  false  "all (default) or following"
// @Param        fromDate  query  string  false  "YYYY-MM-DD, with scope=following"
// @Success      204
// @Failure      404  {object}  ErrorResponse
// @Router       /recurring-rules/{id} [delete]
func (h *RecurringHandler) DeleteRule(c *gin.Context) {
	id, ok := pathID(c, apierr.RecurringRuleNotFound)
	if !ok {
		return
	}
	from, ok := scopeDate(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteRule(id, c.MustGet("userID").(uint), from, userToday(c)); err != nil {
		respondRecurringError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// CancelOccurrence godoc
// @Summary      Cancel one occurrence of a recurring block
// @Description  Deletes it unless it already happened (done/partial) and records a cancelled exception; never marks it skipped.
// @Tags         Recurring
// @Security     BearerAuth
// @Param        id  path  int  true  "Block ID"
// @Success      204
// @Failure      404  {object}  ErrorResponse
// @Router       /tasks/{id}/occurrence [delete]
func (h *RecurringHandler) CancelOccurrence(c *gin.Context) {
	id, ok := pathID(c, apierr.OccurrenceNotFound)
	if !ok {
		return
	}
	if err := h.svc.CancelOccurrence(id, c.MustGet("userID").(uint)); err != nil {
		respondRecurringError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// PlaceAnyway godoc
// @Summary      Place a conflicting occurrence anyway
// @Description  Creates the occurrence that a clash left out (a conflict exception), overlapping on purpose.
// @Tags         Recurring
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      int     true  "Rule ID"
// @Param        date  path      string  true  "YYYY-MM-DD"
// @Success      201   {object}  BlockResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      404   {object}  ErrorResponse
// @Router       /recurring-rules/{id}/occurrences/{date}/place-anyway [post]
func (h *RecurringHandler) PlaceAnyway(c *gin.Context) {
	id, ok := pathID(c, apierr.RecurringRuleNotFound)
	if !ok {
		return
	}
	t, err := h.svc.PlaceAnyway(id, c.MustGet("userID").(uint), c.Param("date"))
	if err != nil {
		respondRecurringError(c, err)
		return
	}
	c.JSON(http.StatusCreated, BlockResponse{Task: *t})
}

// SkipConflict godoc
// @Summary      Skip a conflicting occurrence this week
// @Description  Turns a conflict exception into a cancellation; nothing is counted as missed.
// @Tags         Recurring
// @Security     BearerAuth
// @Param        id    path  int     true  "Rule ID"
// @Param        date  path  string  true  "YYYY-MM-DD"
// @Success      204
// @Failure      400  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Router       /recurring-rules/{id}/occurrences/{date}/skip [post]
func (h *RecurringHandler) SkipConflict(c *gin.Context) {
	id, ok := pathID(c, apierr.RecurringRuleNotFound)
	if !ok {
		return
	}
	if err := h.svc.SkipConflict(id, c.MustGet("userID").(uint), c.Param("date")); err != nil {
		respondRecurringError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Range serves GET /tasks?startDate=&endDate= (documented on TaskHandler.GetTasks): with a range of
// at most 42 days it creates the missing occurrences from today up to 8 weeks ahead, then returns
// the range; `truncated` when the range reaches past what is planned.
func (h *RecurringHandler) Range(c *gin.Context) {
	res, err := h.svc.Range(c.MustGet("userID").(uint), c.Query("startDate"), c.Query("endDate"), userToday(c))
	if err != nil {
		respondRecurringError(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// WithRange serves GET /tasks?startDate=&endDate= with ranged and anything else with plain.
func WithRange(ranged, plain gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := c.GetQuery("startDate"); ok {
			ranged(c)
			return
		}
		if _, ok := c.GetQuery("endDate"); ok {
			ranged(c)
			return
		}
		plain(c)
	}
}

// RegisterRecurringRoutes adds the recurring endpoints (FEATURE_F13). GET /tasks is wrapped with
// WithRange by the caller.
func RegisterRecurringRoutes(r gin.IRoutes, h *RecurringHandler) {
	r.GET("/recurring-rules", h.ListRules)
	r.POST("/recurring-rules", h.CreateRule)
	r.PUT("/recurring-rules/:id", h.UpdateRule)
	r.DELETE("/recurring-rules/:id", h.DeleteRule)
	r.DELETE("/tasks/:id/occurrence", h.CancelOccurrence)
	r.POST("/recurring-rules/:id/occurrences/:date/place-anyway", h.PlaceAnyway)
	r.POST("/recurring-rules/:id/occurrences/:date/skip", h.SkipConflict)
}
