package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/apierr"
	"discipline-backend/internal/middleware"
	"discipline-backend/internal/ratelimit"
	"discipline-backend/internal/repositories"
	"discipline-backend/internal/services"
)

// Calendar import rate limits (F9a §3.3), keyed by user. Outbound fetches fail closed.
var (
	CalendarFetchMinute = middleware.Policy{Name: "calendar_fetch", Limit: 5, Window: time.Minute, FailClosed: true}
	CalendarFetchDay    = middleware.Policy{Name: "calendar_fetch_day", Limit: 30, Window: 24 * time.Hour, FailClosed: true}
	CalendarParse       = middleware.Policy{Name: "calendar_parse", Limit: 20, Window: time.Minute}
)

// CalendarHandler serves calendar import and linked sources (FEATURE_F9A).
type CalendarHandler struct {
	svc    services.CalendarService
	limits ratelimit.Store
}

func NewCalendarHandler(svc services.CalendarService, limits ratelimit.Store) *CalendarHandler {
	return &CalendarHandler{svc: svc, limits: limits}
}

// limit applies one policy; false means the response was written.
func (h *CalendarHandler) limit(c *gin.Context, p middleware.Policy) bool {
	allowed, retry, err := h.limits.Hit(c.Request.Context(), fmt.Sprintf("rl:%s:user:%d", p.Name, c.MustGet("userID").(uint)), p.Limit, p.Window)
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "rate limit store unavailable", "policy", p.Name)
		if p.FailClosed {
			apierr.JSON(c, http.StatusServiceUnavailable, apierr.ServiceUnavailable, "Service temporarily unavailable. Please try again shortly.")
			return false
		}
		return true
	}
	if !allowed {
		c.Header("Retry-After", middleware.RetryAfterSeconds(retry))
		apierr.JSON(c, http.StatusTooManyRequests, apierr.RateLimited, "Too many requests. Please try again later.")
		return false
	}
	return true
}

func respondCalendarError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, services.ErrImport):
		code, _ := apierr.CodeOf(err)
		status := http.StatusBadRequest
		if code == apierr.ICSBusy {
			status = http.StatusServiceUnavailable
		}
		apierr.JSON(c, status, code, err.Error()) // never the URL, an IP or a remote body
	case errors.Is(err, repositories.ErrSourceNotFound):
		apierr.JSON(c, http.StatusNotFound, apierr.CalendarSourceNotFound, "Linked calendar not found")
	case errors.Is(err, repositories.ErrSourceLimit):
		apierr.JSON(c, http.StatusBadRequest, apierr.CalendarSourceLimit, "At most 5 linked calendars")
	default:
		respondCommitmentError(c, err)
	}
}

// Preview godoc
// @Summary  Preview a calendar import (F9a): multipart `file` (<= 1 MiB), or JSON {url} / {sourceId}; saves nothing
// @Tags     Calendar import
// @Accept   json
// @Produce  json
// @Security BearerAuth
// @Success  200 {object} services.Preview
// @Failure  400 {object} ErrorResponse
// @Router   /calendar-import/preview [post]
func (h *CalendarHandler) Preview(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	userID := c.MustGet("userID").(uint)
	loc, today := userLocation(c), userToday(c)
	ctx := c.Request.Context()
	if strings.HasPrefix(c.GetHeader("Content-Type"), "multipart/form-data") {
		if !h.limit(c, CalendarParse) {
			return
		}
		fh, err := c.FormFile("file")
		if err != nil {
			apierr.JSON(c, http.StatusBadRequest, apierr.ValidationFailed, "Send the calendar as a file named file")
			return
		}
		if fh.Size > services.MaxICSFile {
			apierr.JSON(c, http.StatusBadRequest, apierr.ICSFileTooLarge, "The file is larger than 1 MiB")
			return
		}
		f, err := fh.Open()
		if err != nil {
			apierr.JSON(c, http.StatusBadRequest, apierr.ICSInvalid, "The file could not be read")
			return
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, services.MaxICSFile+1))
		if err != nil {
			apierr.JSON(c, http.StatusBadRequest, apierr.ICSInvalid, "The file could not be read")
			return
		}
		p, err := h.svc.PreviewFile(ctx, userID, data, loc, today) // parsed in memory, never stored
		if err != nil {
			respondCalendarError(c, err)
			return
		}
		c.JSON(http.StatusOK, p)
		return
	}
	var req struct {
		URL      string `json:"url"`
		SourceID *uint  `json:"sourceId"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil || (req.URL == "") == (req.SourceID == nil) {
		apierr.JSON(c, http.StatusBadRequest, apierr.ValidationFailed, "Send a file, or one of url and sourceId")
		return
	}
	if !h.limit(c, CalendarFetchMinute) || !h.limit(c, CalendarFetchDay) {
		return
	}
	var (
		p   *services.Preview
		err error
	)
	if req.SourceID != nil {
		p, err = h.svc.PreviewSource(ctx, userID, *req.SourceID, loc, today)
	} else {
		p, err = h.svc.PreviewURL(ctx, userID, req.URL, loc, today)
	}
	if err != nil {
		respondCalendarError(c, err)
		return
	}
	c.JSON(http.StatusOK, p)
}

// CommitRequest is the body of POST /calendar-import/commit.
type CommitRequest struct {
	URL      string `json:"url"`
	SourceID *uint  `json:"sourceId"`
	Origin   string `json:"origin"`
	Items    []struct {
		Key string `json:"key"`
		CommitmentRequest
	} `json:"items" binding:"max=200"`
	RemoveIDs []uint `json:"removeIds"`
}

// Commit godoc
// @Summary  Save the ticked candidates of a preview in one transaction (F9a); the URL is not fetched again
// @Tags     Calendar import
// @Accept   json
// @Produce  json
// @Security BearerAuth
// @Param    body body CommitRequest true "Items"
// @Success  200 {object} services.CommitResult
// @Failure  400 {object} ErrorResponse
// @Router   /calendar-import/commit [post]
func (h *CalendarHandler) Commit(c *gin.Context) {
	key, ok := optionalIdempotencyKey(c)
	if !ok || !h.limit(c, CalendarParse) {
		return
	}
	var req CommitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.JSON(c, http.StatusBadRequest, apierr.ValidationFailed, "Validation failed")
		return
	}
	in := services.CommitInput{URL: req.URL, SourceID: req.SourceID, Origin: req.Origin, RemoveIDs: req.RemoveIDs}
	for _, it := range req.Items {
		in.Items = append(in.Items, services.CommitItem{Key: it.Key, CommitmentInput: it.input()})
	}
	res, err := h.svc.Commit(c.MustGet("userID").(uint), in, key)
	if err != nil {
		respondCalendarError(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// Sources godoc
// @Summary  Linked calendars (F9a): host, last refresh, status; never the URL
// @Tags     Calendar import
// @Produce  json
// @Security BearerAuth
// @Success  200 {object} map[string]interface{}
// @Router   /calendar-sources [get]
func (h *CalendarHandler) Sources(c *gin.Context) {
	list, err := h.svc.Sources(c.MustGet("userID").(uint))
	if err != nil {
		respondCalendarError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sources": list})
}

// DeleteSource godoc
// @Summary  Unlink a calendar (F9a); withCommitments=true also removes what it imported
// @Tags     Calendar import
// @Security BearerAuth
// @Param    id              path  int    true  "Source ID"
// @Param    withCommitments query string false "true|false (default false: they stay as manual ones)"
// @Success  204
// @Failure  404 {object} ErrorResponse
// @Router   /calendar-sources/{id} [delete]
func (h *CalendarHandler) DeleteSource(c *gin.Context) {
	id, ok := pathID(c, apierr.CalendarSourceNotFound)
	if !ok {
		return
	}
	if err := h.svc.DeleteSource(c.MustGet("userID").(uint), id, c.Query("withCommitments") == "true"); err != nil {
		respondCalendarError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// RegisterCalendarRoutes adds the calendar import routes (FEATURE_F9A).
func RegisterCalendarRoutes(r gin.IRoutes, h *CalendarHandler) {
	r.POST("/calendar-import/preview", h.Preview)
	r.POST("/calendar-import/commit", h.Commit)
	r.GET("/calendar-sources", h.Sources)
	r.DELETE("/calendar-sources/:id", h.DeleteSource)
}
