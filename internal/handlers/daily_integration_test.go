package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"discipline-backend/internal/contracttest"
	"discipline-backend/internal/events"
	"discipline-backend/internal/models"
	"discipline-backend/internal/repositories"
	"discipline-backend/internal/services"
	"discipline-backend/internal/testdb"
)

// dailyEnv is one user with a time zone, and the daily-loop routes (check-ins, sessions, shutdown).
type dailyEnv struct {
	r      *gin.Engine
	db     *gorm.DB
	userID uint
}

func newDailyEnv(t *testing.T, tz string) *dailyEnv {
	t.Helper()
	gdb, _ := testdb.NewMigrated(t)
	u := &models.User{Name: "U", Email: "u@example.com", Password: "x", Role: "user", Timezone: tz}
	if err := gdb.Create(u).Error; err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", u.ID)
		c.Set("userTimezone", tz)
	})
	RegisterDailyRoutes(r.Group(""), NewDailyHandler(services.NewDailyService(repositories.NewDailyRepository(gdb), events.NewRecorder(gdb))))
	return &dailyEnv{r: r, db: gdb, userID: u.ID}
}

// at freezes the clock at a wall-clock time in tz for the rest of the test.
func at(t *testing.T, tz, wall string) {
	t.Helper()
	loc, _ := time.LoadLocation(tz)
	tm, err := time.ParseInLocation("2006-01-02 15:04", wall, loc)
	if err != nil {
		t.Fatal(err)
	}
	old := nowFunc
	nowFunc = func() time.Time { return tm }
	t.Cleanup(func() { nowFunc = old })
}

func (e *dailyEnv) call(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestMorningCheckin_HalfPastMidnightBelongsToTheNewDay(t *testing.T) {
	const tz = "Asia/Tashkent"
	e := newDailyEnv(t, tz)
	at(t, tz, "2026-10-30 00:30")
	code, body := e.call(t, http.MethodPut, "/checkins/today",
		`{"sleepStartMin":1410,"sleepEndMin":420,"sleepQuality":4,"energy":3,"note":"ok"}`)
	if code != http.StatusOK {
		t.Fatalf("put: %d %v", code, body)
	}
	c := body["checkin"].(map[string]any)
	if c["date"] != "2026-10-30" || c["sleepMinutes"] != float64(450) {
		t.Fatalf("want 2026-10-30 and 7h30 of sleep (23:30 the evening before -> 07:00), got %v", c)
	}
	raw, _ := json.Marshal(body)
	contracttest.Validate(t, "daily", "checkin_response", raw)

	at(t, tz, "2026-10-30 21:00") // editable the same day
	if code, body = e.call(t, http.MethodPut, "/checkins/today", `{"sleepStartMin":1380,"sleepEndMin":420,"sleepQuality":5,"energy":4}`); code != http.StatusOK {
		t.Fatalf("edit: %d %v", code, body)
	}
	at(t, tz, "2026-10-31 06:00") // the next day starts empty, prefilled from the last check-in
	code, body = e.call(t, http.MethodGet, "/checkins/today", "")
	if code != http.StatusOK || body["checkin"] != nil || body["date"] != "2026-10-31" {
		t.Fatalf("a new day has no check-in yet, got %d %v", code, body)
	}
	defaults := body["defaults"].(map[string]any)
	if defaults["sleepStartMin"] != float64(1380) || defaults["sleepEndMin"] != float64(420) {
		t.Fatalf("defaults come from the last check-in, got %v", defaults)
	}
	raw, _ = json.Marshal(body)
	contracttest.Validate(t, "daily", "checkin_today", raw)
}

func TestMorningCheckin_SleepAcrossTheSpringForwardNight(t *testing.T) {
	const tz = "Europe/Berlin" // clocks jump 02:00 -> 03:00 on 2026-03-29
	e := newDailyEnv(t, tz)
	at(t, tz, "2026-03-29 08:00")
	_, body := e.call(t, http.MethodPut, "/checkins/today", `{"sleepStartMin":1380,"sleepEndMin":420,"sleepQuality":3,"energy":3}`)
	if got := body["checkin"].(map[string]any)["sleepMinutes"]; got != float64(420) {
		t.Fatalf("23:00 -> 07:00 on the DST night is 7 h of real time, got %v minutes", got)
	}
}

func TestMorningCheckin_SleepAcrossTheFallBackNight_AmericaNewYork(t *testing.T) {
	const tz = "America/New_York" // clocks fall back 02:00 -> 01:00 on 2026-11-01
	e := newDailyEnv(t, tz)
	at(t, tz, "2026-11-01 08:00")
	_, body := e.call(t, http.MethodPut, "/checkins/today", `{"sleepStartMin":1380,"sleepEndMin":420,"sleepQuality":4,"energy":4}`)
	if got := body["checkin"].(map[string]any)["sleepMinutes"]; got != float64(540) {
		t.Fatalf("23:00 -> 07:00 on the autumn DST fall-back night is 9 h of real time, got %v minutes", got)
	}
}

func TestMorningCheckin_SkipAndDefaultsForANewUser(t *testing.T) {
	const tz = "Asia/Tashkent"
	e := newDailyEnv(t, tz)
	at(t, tz, "2026-10-30 08:00")
	_, body := e.call(t, http.MethodGet, "/checkins/today", "")
	d := body["defaults"].(map[string]any)
	if d["sleepStartMin"] != float64(1380) || d["sleepEndMin"] != float64(420) {
		t.Fatalf("a new user gets 23:00-07:00, got %v", d)
	}
	code, body := e.call(t, http.MethodPut, "/checkins/today", `{"skipped":true}`)
	if code != http.StatusOK || body["checkin"].(map[string]any)["skipped"] != true {
		t.Fatalf("skip: %d %v", code, body)
	}
}

func TestMorningCheckin_RejectsBadValues(t *testing.T) {
	e := newDailyEnv(t, "UTC")
	for body, code := range map[string]string{
		`{"sleepStartMin":1440,"sleepEndMin":420,"sleepQuality":3,"energy":3}`:  "checkin_invalid",
		`{"sleepStartMin":1380,"sleepEndMin":420,"sleepQuality":0,"energy":3}`:  "checkin_invalid",
		`{"sleepStartMin":1380,"sleepEndMin":420,"sleepQuality":3,"energy":6}`:  "checkin_invalid",
		`{"sleepStartMin":1380,"sleepQuality":3,"energy":3}`:                    "checkin_invalid",
		`{"sleepStartMin":1380,"sleepEndMin":1380,"sleepQuality":3,"energy":3}`: "checkin_invalid",
	} {
		c, out := e.call(t, http.MethodPut, "/checkins/today", body)
		if c != http.StatusBadRequest || out["code"] != code {
			t.Errorf("%s: want 400 %s, got %d %v", body, code, c, out)
		}
	}
}

const sessionA = "6f1c2a7e-6a49-4a43-9c55-0d6f2b6a9f10"

func session(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	s, ok := body["session"].(map[string]any)
	if !ok {
		t.Fatalf("no session in %v", body)
	}
	return s
}

func TestFocusSession_StartIsIdempotentAndCarriesServerTime(t *testing.T) {
	const tz = "Asia/Tashkent"
	e := newDailyEnv(t, tz)
	at(t, tz, "2026-10-30 09:00")
	start := `{"plannedMinutes":25,"breakMinutes":5}`
	code, first := e.call(t, http.MethodPut, "/sessions/"+sessionA, start)
	if code != http.StatusOK {
		t.Fatalf("start: %d %v", code, first)
	}
	raw, _ := json.Marshal(first)
	contracttest.Validate(t, "daily", "session_response", raw)
	s := session(t, first)
	if s["startedAt"] != "2026-10-30T04:00:00Z" || first["serverNow"] != "2026-10-30T04:00:00Z" || s["endedAt"] != nil {
		t.Fatalf("the server stamps the start: %v", first)
	}

	at(t, tz, "2026-10-30 09:01") // a replay (the client didn't see the answer) changes nothing
	_, again := e.call(t, http.MethodPut, "/sessions/"+sessionA, start)
	if session(t, again)["startedAt"] != "2026-10-30T04:00:00Z" {
		t.Fatalf("a replayed start keeps the first start time, got %v", again)
	}
	_, active := e.call(t, http.MethodGet, "/sessions/active", "")
	if session(t, active)["id"] != sessionA || active["serverNow"] != "2026-10-30T04:01:00Z" {
		t.Fatalf("every device reads the same session and the server time, got %v", active)
	}
}

func TestFocusSession_OfflineReplayLeavesExactlyOneSessionWithTheRightData(t *testing.T) {
	const tz = "Asia/Tashkent"
	e := newDailyEnv(t, tz)
	at(t, tz, "2026-10-30 10:00") // reconnect time; the actions happened offline at 09:00-09:25
	queue := []string{
		`{"plannedMinutes":25,"breakMinutes":5,"startedAt":"2026-10-30T04:00:00Z"}`,
		`{"plannedMinutes":25,"breakMinutes":5,"startedAt":"2026-10-30T04:00:00Z","distractions":1}`,
		`{"plannedMinutes":25,"breakMinutes":5,"startedAt":"2026-10-30T04:00:00Z","distractions":2}`,
		`{"plannedMinutes":25,"breakMinutes":5,"startedAt":"2026-10-30T04:00:00Z","distractions":2,"endedAt":"2026-10-30T04:25:00Z","outcome":"completed"}`,
	}
	for round := 0; round < 2; round++ { // the whole queue is sent twice (duplicate submits)
		for _, body := range queue {
			if code, out := e.call(t, http.MethodPut, "/sessions/"+sessionA, body); code != http.StatusOK {
				t.Fatalf("round %d: %d %v", round, code, out)
			}
		}
	}
	code, list := e.call(t, http.MethodGet, "/sessions?date=2026-10-30", "")
	sessions := list["sessions"].([]any)
	if code != http.StatusOK || len(sessions) != 1 {
		t.Fatalf("want exactly one session, got %d %v", code, list)
	}
	s := sessions[0].(map[string]any)
	if s["startedAt"] != "2026-10-30T04:00:00Z" || s["endedAt"] != "2026-10-30T04:25:00Z" ||
		s["outcome"] != "completed" || s["distractions"] != float64(2) {
		t.Fatalf("unexpected session %v", s)
	}
	raw, _ := json.Marshal(list)
	contracttest.Validate(t, "daily", "session_list", raw)
}

func TestFocusSession_OneActiveAtATimeAndValidation(t *testing.T) {
	const tz = "UTC"
	e := newDailyEnv(t, tz)
	at(t, tz, "2026-10-30 09:00")
	e.call(t, http.MethodPut, "/sessions/"+sessionA, `{"plannedMinutes":50,"breakMinutes":10}`)
	code, out := e.call(t, http.MethodPut, "/sessions/7d3a1b1c-1111-4c3b-8a1a-2e5b7c9d0e11", `{"plannedMinutes":25,"breakMinutes":5}`)
	if code != http.StatusConflict || out["code"] != "session_active" {
		t.Fatalf("a second running session: want 409 session_active, got %d %v", code, out)
	}
	for _, body := range []string{
		`{"plannedMinutes":9,"breakMinutes":5}`, `{"plannedMinutes":181,"breakMinutes":5}`,
		`{"plannedMinutes":25,"breakMinutes":61}`, `{"plannedMinutes":25,"breakMinutes":5,"outcome":"done"}`,
		`{"plannedMinutes":25,"breakMinutes":5,"endedAt":"2026-10-30T09:20:00Z"}`,
	} {
		if code, out := e.call(t, http.MethodPut, "/sessions/1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed", body); code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d %v", body, code, out)
		}
	}
	if code, _ := e.call(t, http.MethodPut, "/sessions/not-a-uuid", `{"plannedMinutes":25,"breakMinutes":5}`); code != http.StatusBadRequest {
		t.Errorf("the id must be a UUID, got %d", code)
	}
}

func TestFocusSession_ForgottenSessionIsAbandonedWhenRead(t *testing.T) {
	const tz = "UTC"
	e := newDailyEnv(t, tz)
	at(t, tz, "2026-10-30 09:00")
	e.call(t, http.MethodPut, "/sessions/"+sessionA, `{"plannedMinutes":25,"breakMinutes":5}`)
	at(t, tz, "2026-10-30 10:26") // 25 min + 60 min grace have passed
	_, out := e.call(t, http.MethodGet, "/sessions/active", "")
	if out["session"] != nil {
		t.Fatalf("a session left running past its time + 60 min is not active, got %v", out)
	}
	_, list := e.call(t, http.MethodGet, "/sessions?date=2026-10-30", "")
	s := list["sessions"].([]any)[0].(map[string]any)
	if s["outcome"] != "abandoned" || s["endedAt"] != "2026-10-30T09:25:00Z" {
		t.Fatalf("it ends as abandoned at its planned end, got %v", s)
	}
}

func TestParkingLot_NoteIsStoredOnce(t *testing.T) {
	e := newDailyEnv(t, "UTC")
	for range 2 {
		if code, out := e.call(t, http.MethodPut, "/parking-lot/0f8fad5b-d9cb-469f-a165-70867728950e", `{"text":"Email the professor"}`); code != http.StatusOK {
			t.Fatalf("park: %d %v", code, out)
		}
	}
	_, out := e.call(t, http.MethodGet, "/parking-lot", "")
	if items := out["items"].([]any); len(items) != 1 || items[0].(map[string]any)["text"] != "Email the professor" {
		t.Fatalf("want one note, got %v", out)
	}
	if code, _ := e.call(t, http.MethodPut, "/parking-lot/0f8fad5b-d9cb-469f-a165-70867728950f", `{"text":"  "}`); code != http.StatusBadRequest {
		t.Fatalf("an empty note is rejected, got %d", code)
	}
}

func TestShutdown_ClosesTheLocalDayOnce(t *testing.T) {
	const tz = "Asia/Tashkent"
	e := newDailyEnv(t, tz)
	at(t, tz, "2026-10-30 21:30")
	code, body := e.call(t, http.MethodGet, "/shutdowns/today", "")
	if code != http.StatusOK || body["shutdown"] != nil || body["date"] != "2026-10-30" {
		t.Fatalf("open day: %d %v", code, body)
	}
	for range 2 {
		code, body = e.call(t, http.MethodPut, "/shutdowns/today", `{"win":"Finished the essay draft"}`)
		if code != http.StatusOK {
			t.Fatalf("shutdown: %d %v", code, body)
		}
	}
	raw, _ := json.Marshal(body)
	contracttest.Validate(t, "daily", "shutdown_response", raw)
	s := body["shutdown"].(map[string]any)
	if s["date"] != "2026-10-30" || s["win"] != "Finished the essay draft" || s["closedAt"] != "2026-10-30T16:30:00Z" {
		t.Fatalf("unexpected %v", s)
	}
	_, body = e.call(t, http.MethodGet, "/shutdowns/today", "")
	if body["shutdown"] == nil {
		t.Fatal("the day stays closed")
	}
	raw, _ = json.Marshal(body)
	contracttest.Validate(t, "daily", "shutdown_today", raw)
	if code, out := e.call(t, http.MethodPut, "/shutdowns/today", `{"win":"`+strings.Repeat("x", 281)+`"}`); code != http.StatusBadRequest || out["code"] != "shutdown_invalid" {
		t.Fatalf("a win is at most 280 characters, got %d %v", code, out)
	}
}

func TestFocusSession_StartingInABlockMarksItInProgress(t *testing.T) {
	const tz = "UTC"
	e := newDailyEnv(t, tz)
	at(t, tz, "2026-10-30 09:00")
	gdb := e.db
	block := &models.Task{UserID: e.userID, Name: "Essay", Category: "study", StartTime: "09:00", EndTime: "10:00", Date: "2026-10-30", Energy: 3}
	if err := gdb.Create(block).Error; err != nil {
		t.Fatal(err)
	}
	if code, out := e.call(t, http.MethodPut, "/sessions/"+sessionA, fmt.Sprintf(`{"blockId":%d,"plannedMinutes":50,"breakMinutes":10}`, block.ID)); code != http.StatusOK {
		t.Fatalf("start: %d %v", code, out)
	}
	var stored models.Task
	gdb.First(&stored, block.ID)
	if stored.Status != models.StatusInProgress {
		t.Fatalf("the block is in progress once a session starts in it, got %s", stored.Status)
	}
}

func eventNames(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var names []string
	if err := db.Raw(`SELECT name FROM product_events ORDER BY id`).Scan(&names).Error; err != nil {
		t.Fatal(err)
	}
	return names
}

func TestDailyLoopEvents_AreWrittenOnceEvenWhenRequestsAreReplayed(t *testing.T) {
	const tz = "UTC"
	e := newDailyEnv(t, tz)
	at(t, tz, "2026-10-30 09:00")
	for range 2 {
		e.call(t, http.MethodPut, "/checkins/today", `{"sleepStartMin":1380,"sleepEndMin":420,"sleepQuality":4,"energy":4}`)
		e.call(t, http.MethodPut, "/sessions/"+sessionA, `{"plannedMinutes":25,"breakMinutes":5}`)
		e.call(t, http.MethodPut, "/sessions/"+sessionA, `{"plannedMinutes":25,"breakMinutes":5,"distractions":1}`)
		e.call(t, http.MethodPut, "/sessions/"+sessionA, `{"plannedMinutes":25,"breakMinutes":5,"distractions":1,"endedAt":"2026-10-30T09:25:00Z","outcome":"completed"}`)
		e.call(t, http.MethodPut, "/shutdowns/today", `{"win":"x"}`)
	}
	got := strings.Join(eventNames(t, e.db), ",")
	want := "checkin_morning_done,session_started,distraction_logged,session_completed,shutdown_done"
	if got != want {
		t.Fatalf("want %s, got %s", want, got)
	}
	var props string
	e.db.Raw(`SELECT props::text FROM product_events WHERE name = 'session_completed'`).Scan(&props)
	if !strings.Contains(props, `"planned_minutes": 25`) || strings.Contains(props, "@") {
		t.Fatalf("props carry facts, never personal data: %s", props)
	}
}

// A check-in made just before midnight and synced after it counts for the day it was made.
func TestMorningCheckin_QueuedOfflineCountsForTheLocalDayItWasMade(t *testing.T) {
	const tz = "Asia/Tashkent"
	e := newDailyEnv(t, tz)
	at(t, tz, "2026-10-30 00:05") // the queue drains just after midnight
	code, body := e.call(t, http.MethodPut, "/checkins/today",
		`{"sleepStartMin":1410,"sleepEndMin":420,"sleepQuality":4,"energy":3,"madeAt":"2026-10-29T23:58:00+05:00"}`)
	if code != http.StatusOK {
		t.Fatalf("put: %d %v", code, body)
	}
	if got := body["checkin"].(map[string]any)["date"]; got != "2026-10-29" {
		t.Fatalf("want the check-in on the day it was made (2026-10-29), got %v", got)
	}
	raw, _ := json.Marshal(body)
	contracttest.Validate(t, "daily", "checkin_response", raw)

	// Today is still open, so today's prompt comes back.
	if code, body = e.call(t, http.MethodGet, "/checkins/today", ""); code != http.StatusOK ||
		body["date"] != "2026-10-30" || body["checkin"] != nil {
		t.Fatalf("today must still be open, got %d %v", code, body)
	}

	// A replay of the same queued request changes nothing and stays on the same day.
	if code, body = e.call(t, http.MethodPut, "/checkins/today",
		`{"sleepStartMin":1410,"sleepEndMin":420,"sleepQuality":4,"energy":3,"madeAt":"2026-10-29T23:58:00+05:00"}`); code != http.StatusOK ||
		body["checkin"].(map[string]any)["date"] != "2026-10-29" {
		t.Fatalf("replay: %d %v", code, body)
	}
	var days []string
	if err := e.db.Raw(`SELECT date::text FROM checkins WHERE user_id = ? ORDER BY date`, e.userID).Scan(&days).Error; err != nil {
		t.Fatal(err)
	}
	if len(days) != 1 || days[0] != "2026-10-29" {
		t.Fatalf("want exactly one check-in on 2026-10-29, got %v", days)
	}
}

// A device clock outside the bounds a queued session start uses falls back to today.
func TestMorningCheckin_UnbelievableClientClockFallsBackToToday(t *testing.T) {
	const tz = "Asia/Tashkent"
	for _, c := range []struct{ name, madeAt string }{
		{"days old", "2026-10-20T10:00:00+05:00"},
		{"tomorrow (clock skew)", "2026-10-31T09:00:00+05:00"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newDailyEnv(t, tz)
			at(t, tz, "2026-10-30 00:05")
			code, body := e.call(t, http.MethodPut, "/checkins/today",
				fmt.Sprintf(`{"sleepStartMin":1410,"sleepEndMin":420,"sleepQuality":4,"energy":3,"madeAt":%q}`, c.madeAt))
			if code != http.StatusOK {
				t.Fatalf("put: %d %v", code, body)
			}
			if got := body["checkin"].(map[string]any)["date"]; got != "2026-10-30" {
				t.Fatalf("want today (2026-10-30), got %v", got)
			}
		})
	}
}
