package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/apierr"
	"discipline-backend/internal/models"
	"discipline-backend/internal/services"
)

// DailyHandler serves the daily loop (F1): morning check-in, focus sessions, evening shutdown.
type DailyHandler struct {
	daily services.DailyService
}

func NewDailyHandler(daily services.DailyService) *DailyHandler {
	return &DailyHandler{daily: daily}
}

// RegisterDailyRoutes adds the daily-loop routes to an authenticated group.
func RegisterDailyRoutes(g *gin.RouterGroup, h *DailyHandler) {
	g.GET("/checkins/today", h.GetCheckin)
	g.PUT("/checkins/today", h.SaveCheckin)
	g.GET("/sessions/active", h.ActiveSession)
	g.GET("/sessions", h.ListSessions)
	g.PUT("/sessions/:id", h.SaveSession)
	g.GET("/parking-lot", h.ListParkingNotes)
	g.PUT("/parking-lot/:id", h.ParkNote)
	g.GET("/shutdowns/today", h.GetShutdown)
	g.PUT("/shutdowns/today", h.CloseDay)
}

// respondDailyError maps coded validation errors to 400, an active session to 409 and anything else to 500.
func respondDailyError(c *gin.Context, err error) {
	code, ok := apierr.CodeOf(err)
	switch {
	case !ok:
		apierr.JSON(c, http.StatusInternalServerError, apierr.InternalError, "Failed to save")
	case errors.Is(err, services.ErrSessionConflict):
		apierr.JSON(c, http.StatusConflict, code, err.Error())
	default:
		apierr.JSON(c, http.StatusBadRequest, code, err.Error())
	}
}

// SleepDefaults are the sleep times to prefill.
type SleepDefaults struct {
	SleepStartMin int `json:"sleepStartMin" example:"1380"`
	SleepEndMin   int `json:"sleepEndMin" example:"420"`
}

// CheckinTodayResponse is today's check-in (null until done) and the prefill.
type CheckinTodayResponse struct {
	Date     string          `json:"date" example:"2026-10-30"`
	Checkin  *models.Checkin `json:"checkin"`
	Defaults SleepDefaults   `json:"defaults"`
}

// CheckinResponse wraps a saved check-in.
type CheckinResponse struct {
	Checkin *models.Checkin `json:"checkin"`
}

// GetCheckin godoc
// @Summary      Today's morning check-in
// @Description  "Today" is the user's local date. checkin is null until done or skipped; defaults prefill the sleep times.
// @Tags         Daily
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  CheckinTodayResponse
// @Router       /checkins/today [get]
func (h *DailyHandler) GetCheckin(c *gin.Context) {
	out, err := h.daily.GetCheckin(c.MustGet("userID").(uint), models.Date(userToday(c)), userLocation(c))
	if err != nil {
		apierr.JSON(c, http.StatusInternalServerError, apierr.InternalError, "Failed to load the check-in")
		return
	}
	c.JSON(http.StatusOK, CheckinTodayResponse{Date: string(out.Date), Checkin: out.Checkin,
		Defaults: SleepDefaults{SleepStartMin: out.SleepStartMin, SleepEndMin: out.SleepEndMin}})
}

// CheckinRequest is a morning check-in, or {"skipped": true}.
type CheckinRequest struct {
	Skipped       bool    `json:"skipped" example:"false"`
	SleepStartMin *int    `json:"sleepStartMin" example:"1410"` // after sleepEndMin = the evening before
	SleepEndMin   *int    `json:"sleepEndMin" example:"420"`
	SleepQuality  *int    `json:"sleepQuality" example:"4"` // 1-5
	Energy        *int    `json:"energy" example:"3"`       // 1-5
	Note          *string `json:"note" example:"Slept well"`
	// MadeAt is the device clock when the user saved the check-in; an offline queue replays it
	// later. The check-in counts for that local day when the clock is believable (at most 24 h
	// old, at most a minute ahead), otherwise for today.
	MadeAt *time.Time `json:"madeAt,omitempty" example:"2026-10-29T23:58:00Z"`
}

// SaveCheckin godoc
// @Summary      Save today's morning check-in
// @Description  Creates or replaces the check-in for the user's local date (editable the same day). Send {"skipped": true} to skip. With madeAt, a check-in queued offline counts for the local day it was made.
// @Tags         Daily
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CheckinRequest  true  "Check-in"
// @Success      200   {object}  CheckinResponse
// @Failure      400   {object}  ErrorResponse
// @Router       /checkins/today [put]
func (h *DailyHandler) SaveCheckin(c *gin.Context) {
	var req CheckinRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.JSON(c, http.StatusBadRequest, apierr.ValidationFailed, "Validation failed", gin.H{"details": err.Error()})
		return
	}
	loc := userLocation(c)
	saved, err := h.daily.SaveCheckin(c.MustGet("userID").(uint), services.CheckinDay(req.MadeAt, nowFunc(), loc), loc, services.CheckinInput{
		Skipped: req.Skipped, SleepStartMin: req.SleepStartMin, SleepEndMin: req.SleepEndMin,
		SleepQuality: req.SleepQuality, Energy: req.Energy, Note: req.Note,
	})
	if err != nil {
		respondDailyError(c, err)
		return
	}
	c.JSON(http.StatusOK, CheckinResponse{Checkin: saved})
}

// rfc3339 formats an instant as UTC RFC 3339 ("2026-10-30T04:00:00Z").
func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// SessionView is a focus session as clients see it.
type SessionView struct {
	ID             string  `json:"id" example:"6f1c2a7e-6a49-4a43-9c55-0d6f2b6a9f10"`
	BlockID        *uint   `json:"blockId"`
	StartedAt      string  `json:"startedAt" example:"2026-10-30T04:00:00Z"`
	PlannedMinutes int     `json:"plannedMinutes" example:"25"`
	BreakMinutes   int     `json:"breakMinutes" example:"5"`
	EndedAt        *string `json:"endedAt"`
	Outcome        *string `json:"outcome"` // completed | abandoned | null while running
	Distractions   int     `json:"distractions"`
}

func sessionView(s *models.FocusSession) *SessionView {
	if s == nil {
		return nil
	}
	v := &SessionView{ID: s.ID, BlockID: s.BlockID, StartedAt: rfc3339(s.StartedAt), PlannedMinutes: s.PlannedMinutes,
		BreakMinutes: s.BreakMinutes, Outcome: s.Outcome, Distractions: s.Distractions}
	if s.EndedAt != nil {
		e := rfc3339(*s.EndedAt)
		v.EndedAt = &e
	}
	return v
}

// SessionResponse is one session (null if none) and the server's clock, so every device shows
// the same countdown: remaining = plannedMinutes - (serverNow - startedAt).
type SessionResponse struct {
	Session   *SessionView `json:"session"`
	ServerNow string       `json:"serverNow" example:"2026-10-30T04:01:00Z"`
}

// SessionListResponse lists sessions with the server's clock.
type SessionListResponse struct {
	Sessions  []SessionView `json:"sessions"`
	ServerNow string        `json:"serverNow"`
}

// SessionRequest starts or updates a focus session. Values are absolute, so a queue can be resent.
type SessionRequest struct {
	BlockID        *uint      `json:"blockId"`
	StartedAt      *time.Time `json:"startedAt"` // only for a start queued offline
	PlannedMinutes int        `json:"plannedMinutes" example:"25"`
	BreakMinutes   int        `json:"breakMinutes" example:"5"`
	Distractions   int        `json:"distractions" example:"0"`
	EndedAt        *time.Time `json:"endedAt"`
	Outcome        *string    `json:"outcome"` // completed | abandoned
}

// SaveSession godoc
// @Summary      Start or update a focus session
// @Description  The client makes the id (UUID); the same body sent again changes nothing. A new session gets the server's time as its start (or startedAt, for a start queued offline in the last 24 h). One running session per user.
// @Tags         Daily
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string          true  "Session UUID"
// @Param        body  body      SessionRequest  true  "Session"
// @Success      200   {object}  SessionResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse
// @Router       /sessions/{id} [put]
func (h *DailyHandler) SaveSession(c *gin.Context) {
	var req SessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.JSON(c, http.StatusBadRequest, apierr.ValidationFailed, "Validation failed", gin.H{"details": err.Error()})
		return
	}
	now := nowFunc()
	s, err := h.daily.SaveSession(c.MustGet("userID").(uint), c.Param("id"), now, services.SessionInput{
		BlockID: req.BlockID, StartedAt: req.StartedAt, PlannedMinutes: req.PlannedMinutes, BreakMinutes: req.BreakMinutes,
		Distractions: req.Distractions, EndedAt: req.EndedAt, Outcome: req.Outcome,
	})
	if err != nil {
		respondDailyError(c, err)
		return
	}
	c.JSON(http.StatusOK, SessionResponse{Session: sessionView(s), ServerNow: rfc3339(now)})
}

// ActiveSession godoc
// @Summary      The running focus session
// @Description  session is null when none is running. Sessions left running 60 min past their planned end are ended as abandoned.
// @Tags         Daily
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  SessionResponse
// @Router       /sessions/active [get]
func (h *DailyHandler) ActiveSession(c *gin.Context) {
	now := nowFunc()
	s, err := h.daily.ActiveSession(c.MustGet("userID").(uint), now)
	if err != nil {
		apierr.JSON(c, http.StatusInternalServerError, apierr.InternalError, "Failed to load the session")
		return
	}
	c.JSON(http.StatusOK, SessionResponse{Session: sessionView(s), ServerNow: rfc3339(now)})
}

// ListSessions godoc
// @Summary      Focus sessions started on a day
// @Tags         Daily
// @Produce      json
// @Security     BearerAuth
// @Param        date  query     string  false  "YYYY-MM-DD (default: today in the user's zone)"
// @Success      200   {object}  SessionListResponse
// @Failure      400   {object}  ErrorResponse
// @Router       /sessions [get]
func (h *DailyHandler) ListSessions(c *gin.Context) {
	date := c.Query("date")
	if date == "" {
		date = userToday(c)
	}
	now := nowFunc()
	list, err := h.daily.SessionsOn(c.MustGet("userID").(uint), date, userLocation(c), now)
	if err != nil {
		respondDailyError(c, err)
		return
	}
	out := SessionListResponse{Sessions: []SessionView{}, ServerNow: rfc3339(now)}
	for i := range list {
		out.Sessions = append(out.Sessions, *sessionView(&list[i]))
	}
	c.JSON(http.StatusOK, out)
}

// ParkingNoteRequest parks a thought during a session.
type ParkingNoteRequest struct {
	Text      string  `json:"text" example:"Email the professor"`
	SessionID *string `json:"sessionId"`
}

// ParkingNoteView is a parked thought.
type ParkingNoteView struct {
	ID        string  `json:"id"`
	Text      string  `json:"text"`
	SessionID *string `json:"sessionId"`
	CreatedAt string  `json:"createdAt"`
}

// ParkingListResponse lists parked thoughts, oldest first.
type ParkingListResponse struct {
	Items []ParkingNoteView `json:"items"`
}

// ParkNote godoc
// @Summary      Park a thought for later
// @Description  The client makes the id (UUID); sending it again stores nothing new.
// @Tags         Daily
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id    path      string              true  "Note UUID"
// @Param        body  body      ParkingNoteRequest  true  "Note"
// @Success      200   {object}  map[string]bool
// @Failure      400   {object}  ErrorResponse
// @Router       /parking-lot/{id} [put]
func (h *DailyHandler) ParkNote(c *gin.Context) {
	var req ParkingNoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.JSON(c, http.StatusBadRequest, apierr.ValidationFailed, "Validation failed", gin.H{"details": err.Error()})
		return
	}
	if err := h.daily.ParkNote(c.MustGet("userID").(uint), c.Param("id"), req.Text, req.SessionID); err != nil {
		respondDailyError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ListParkingNotes godoc
// @Summary      Parked thoughts
// @Tags         Daily
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  ParkingListResponse
// @Router       /parking-lot [get]
func (h *DailyHandler) ListParkingNotes(c *gin.Context) {
	notes, err := h.daily.ParkingNotes(c.MustGet("userID").(uint))
	if err != nil {
		apierr.JSON(c, http.StatusInternalServerError, apierr.InternalError, "Failed to load notes")
		return
	}
	out := ParkingListResponse{Items: []ParkingNoteView{}}
	for _, n := range notes {
		out.Items = append(out.Items, ParkingNoteView{ID: n.ID, Text: n.Text, SessionID: n.SessionID, CreatedAt: rfc3339(n.CreatedAt)})
	}
	c.JSON(http.StatusOK, out)
}

// ShutdownView is a closed day.
type ShutdownView struct {
	Date     string  `json:"date" example:"2026-10-30"`
	Win      *string `json:"win" example:"Finished the essay draft"`
	ClosedAt string  `json:"closedAt" example:"2026-10-30T16:30:00Z"`
}

func shutdownView(s *models.Shutdown) *ShutdownView {
	if s == nil {
		return nil
	}
	return &ShutdownView{Date: string(s.Date), Win: s.Win, ClosedAt: rfc3339(s.ClosedAt)}
}

// ShutdownTodayResponse is today's local date and its shutdown (null while the day is open).
type ShutdownTodayResponse struct {
	Date     string        `json:"date"`
	Shutdown *ShutdownView `json:"shutdown"`
}

// ShutdownResponse wraps a shutdown.
type ShutdownResponse struct {
	Shutdown *ShutdownView `json:"shutdown"`
}

// ShutdownRequest closes the day with one win (optional, up to 280 characters).
type ShutdownRequest struct {
	Win string `json:"win" example:"Finished the essay draft"`
}

// GetShutdown godoc
// @Summary      Today's evening shutdown
// @Tags         Daily
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  ShutdownTodayResponse
// @Router       /shutdowns/today [get]
func (h *DailyHandler) GetShutdown(c *gin.Context) {
	today := userToday(c)
	s, err := h.daily.GetShutdown(c.MustGet("userID").(uint), models.Date(today))
	if err != nil {
		apierr.JSON(c, http.StatusInternalServerError, apierr.InternalError, "Failed to load the shutdown")
		return
	}
	c.JSON(http.StatusOK, ShutdownTodayResponse{Date: today, Shutdown: shutdownView(s)})
}

// CloseDay godoc
// @Summary      Close today (evening shutdown)
// @Description  Marks the user's local day closed with one win. Sending it again keeps the first close time.
// @Tags         Daily
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      ShutdownRequest  true  "Shutdown"
// @Success      200   {object}  ShutdownResponse
// @Failure      400   {object}  ErrorResponse
// @Router       /shutdowns/today [put]
func (h *DailyHandler) CloseDay(c *gin.Context) {
	var req ShutdownRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apierr.JSON(c, http.StatusBadRequest, apierr.ValidationFailed, "Validation failed", gin.H{"details": err.Error()})
		return
	}
	s, err := h.daily.CloseDay(c.MustGet("userID").(uint), models.Date(userToday(c)), nowFunc(), req.Win)
	if err != nil {
		respondDailyError(c, err)
		return
	}
	c.JSON(http.StatusOK, ShutdownResponse{Shutdown: shutdownView(s)})
}
