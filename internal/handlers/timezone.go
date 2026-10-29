package handlers

import (
	"time"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/models"
)

// nowFunc is the clock; tests replace it.
var nowFunc = time.Now

// userLocation returns the authenticated user's time zone (set by AuthMiddleware).
func userLocation(c *gin.Context) *time.Location {
	name := c.GetString("userTimezone")
	if name == "" {
		name = models.DefaultTimezone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		loc, _ = time.LoadLocation(models.DefaultTimezone)
	}
	return loc
}

// localDate formats the calendar date of instant t in loc as YYYY-MM-DD.
func localDate(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("2006-01-02")
}

// userToday is today's date in the authenticated user's time zone.
func userToday(c *gin.Context) string {
	return localDate(nowFunc(), userLocation(c))
}
