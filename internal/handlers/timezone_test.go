package handlers

import (
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/models"
	"discipline-backend/internal/services"
)

// 2026-10-28 19:30 UTC is 2026-10-29 00:30 in Asia/Tashkent (UTC+5).
var halfPastMidnightTashkent = time.Date(2026, 10, 28, 19, 30, 0, 0, time.UTC)

func freezeNow(t *testing.T, at time.Time) {
	t.Helper()
	old := nowFunc
	nowFunc = func() time.Time { return at }
	t.Cleanup(func() { nowFunc = old })
}

func TestLocalDate_HalfPastMidnightTashkent(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Tashkent")
	if err != nil {
		t.Fatal(err)
	}
	if got := localDate(halfPastMidnightTashkent, loc); got != "2026-10-29" {
		t.Fatalf("00:30 Tashkent must be 2026-10-29, got %s", got)
	}
	if got := localDate(halfPastMidnightTashkent, time.UTC); got != "2026-10-28" {
		t.Fatalf("same instant in UTC must be 2026-10-28, got %s", got)
	}
}

type dateCapturingTaskService struct {
	services.TaskService
	date string
}

func (f *dateCapturingTaskService) GetTasks(userID uint, date string) ([]models.Task, error) {
	f.date = date
	return nil, nil
}
func (f *dateCapturingTaskService) GetDashboardStats(userID uint, date string) (*services.DashboardStats, error) {
	f.date = date
	return &services.DashboardStats{}, nil
}

type dateCapturingVocabService struct {
	services.VocabService
	date string
}

func (f *dateCapturingVocabService) GetTodayWords(userID uint, date string) ([]models.VocabularyWord, error) {
	f.date = date
	return nil, nil
}

func tzRouter(tz string, register func(r *gin.Engine)) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", uint(1))
		c.Set("userTimezone", tz)
	})
	register(r)
	return r
}

func TestDefaultDates_UseUserTimezone(t *testing.T) {
	freezeNow(t, halfPastMidnightTashkent)
	for _, tc := range []struct{ tz, want string }{
		{"Asia/Tashkent", "2026-10-29"},
		{"UTC", "2026-10-28"},
		{"America/New_York", "2026-10-28"},
	} {
		tasks := &dateCapturingTaskService{}
		vocab := &dateCapturingVocabService{}
		r := tzRouter(tc.tz, func(r *gin.Engine) {
			th := NewTaskHandler(tasks)
			r.GET("/tasks", th.GetTasks)
			r.GET("/stats", th.GetDashboardStats)
			r.GET("/vocab", NewVocabHandler(vocab).GetTodayWords)
		})
		doRequest(r, http.MethodGet, "/tasks", "")
		if tasks.date != tc.want {
			t.Errorf("%s: GET /tasks default date = %s, want %s", tc.tz, tasks.date, tc.want)
		}
		doRequest(r, http.MethodGet, "/stats", "")
		if tasks.date != tc.want {
			t.Errorf("%s: GET /dashboard/stats default date = %s, want %s", tc.tz, tasks.date, tc.want)
		}
		doRequest(r, http.MethodGet, "/vocab", "")
		if vocab.date != tc.want {
			t.Errorf("%s: GET /vocabulary/today default date = %s, want %s", tc.tz, vocab.date, tc.want)
		}
	}
}

func TestUpdateProfile_Timezone(t *testing.T) {
	r := realAuthRouter(t)
	registerAnn(t, r)
	if w := doRequest(r, http.MethodPut, "/me", `{"timezone":"Asia/Tokyo"}`); w.Code != http.StatusOK {
		t.Fatalf("valid IANA timezone must be accepted, got %d %s", w.Code, w.Body.String())
	}
	for _, bad := range []string{"Mars/Olympus_Mons", "Local", "+05:00", "../../etc/passwd"} {
		if w := doRequest(r, http.MethodPut, "/me", `{"timezone":"`+bad+`"}`); w.Code != http.StatusBadRequest {
			t.Errorf("timezone %q must be rejected, got %d", bad, w.Code)
		}
	}
}

func TestTodayWords_DateParamValidatedInUserTimezone(t *testing.T) {
	freezeNow(t, halfPastMidnightTashkent) // local date 2026-10-29
	vocab := &dateCapturingVocabService{}
	r := tzRouter("Asia/Tashkent", func(r *gin.Engine) {
		r.GET("/vocab", NewVocabHandler(vocab).GetTodayWords)
	})
	for q, want := range map[string]int{
		"date=2026-10-30": http.StatusOK,         // tomorrow locally
		"date=2026-10-31": http.StatusBadRequest, // two days ahead
		"date=2026-01-01": http.StatusOK,         // past is fine
		"date=2026-02-30": http.StatusBadRequest,
		"date=garbage":    http.StatusBadRequest,
		"date=29.10.2026": http.StatusBadRequest,
	} {
		vocab.date = ""
		w := doRequest(r, http.MethodGet, "/vocab?"+q, "")
		if w.Code != want {
			t.Errorf("%s: expected %d, got %d", q, want, w.Code)
		}
		if want != http.StatusOK && vocab.date != "" {
			t.Errorf("%s: service must not be called for an invalid date", q)
		}
	}
}
