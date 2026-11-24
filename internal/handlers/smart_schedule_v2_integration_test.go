package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"discipline-backend/internal/contracttest"
	"discipline-backend/internal/repositories"
	"discipline-backend/internal/services"
)

// F9a S5: with FEATURE_F9A the browser sends only {weekStart}; the API builds the generator's input
// from the saved commitments and targets and never forwards the browser's body.
func newScheduleEnv(t *testing.T) (*taskEnv, *upstreamRecorder) {
	t.Helper()
	e := newTargetsEnv(t)
	up := &upstreamRecorder{}
	srv := up.server(t)
	h := NewAnalyticsHandler(srv.URL, testInternalSecret).WithScheduleInputs(
		services.NewScheduleInputs(repositories.NewCommitmentRepository(e.db), repositories.NewTargetRepository(e.db), services.NewTargetService(repositories.NewTargetRepository(e.db))))
	e.r.POST("/analytics/smart-schedule", h.GenerateSmartSchedule)
	return e, up
}

func TestSmartScheduleV2_BuildsBusyFromTheServer(t *testing.T) {
	e, up := newScheduleEnv(t)
	createCommitment(t, e, `{"name":"Sleep","kind":"rest","weekdays":[1,2,3,4,5,6,7],"startMin":1380,"endMin":420,"validFrom":"2026-01-01","validTo":null}`)
	createCommitment(t, e, `{"name":"Lectures","kind":"study","weekdays":[1,3],"startMin":540,"endMin":720,"validFrom":"2026-11-18","validTo":"2026-11-18"}`)
	createCommitment(t, e, `{"name":"Old job","kind":"admin","weekdays":[1,2,3,4,5],"startMin":540,"endMin":1080,"validFrom":"2026-01-01","validTo":"2026-11-01"}`)
	deep, study := systemCategory(t, e, "deep_work", ""), systemCategory(t, e, "study", "")
	doRequestAs(e, http.MethodPut, "/weekly-targets", fmt.Sprintf(`{"categories":[{"categoryId":%d,"minutesPerWeek":720},{"categoryId":%d,"minutesPerWeek":330}],"maxFocusMinPerDay":180,"restDay":7}`, deep, study), "")

	w := doRequestAs(e, http.MethodPost, "/analytics/smart-schedule", `{"weekStart":"2026-11-16"}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if err := contracttest.Check("analytics", "smart_schedule_request_v2", up.lastBody); err != nil {
		t.Fatalf("forwarded body breaks the contract: %v\n%s", err, up.lastBody)
	}
	var fwd struct {
		WeekStart string
		Busy      []struct {
			Day      string
			StartMin int
			EndMin   int
			Name     string
			Kind     string
		}
		Goals             map[string]float64
		RestDay           *int
		MaxFocusMinPerDay *int
	}
	_ = json.Unmarshal(up.lastBody, &fwd)
	var busy []string
	for _, b := range fwd.Busy {
		busy = append(busy, fmt.Sprintf("%s %d-%d %s", b.Day, b.StartMin, b.EndMin, b.Name))
	}
	got := strings.Join(busy, "|")
	for _, want := range []string{
		"Monday 0-420 Sleep", "Monday 1380-1440 Sleep", "Tuesday 0-420 Sleep", // Sunday's tail lands on Monday; never Mon 00-07 from Monday itself
		"Wednesday 540-720 Lectures", "Sunday 1380-1440 Sleep",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("busy misses %q: %s", want, got)
		}
	}
	if strings.Contains(got, "Old job") || strings.Contains(got, "Monday 540-720 Lectures") {
		t.Errorf("validity dates of the chosen week must apply: %s", got)
	}
	if fwd.Goals["deepWorkHours"] != 12 || fwd.Goals["studyHours"] != 5.5 || fwd.Goals["restHours"] != 0 {
		t.Errorf("goals from targets by kind: %v", fwd.Goals)
	}
	if fwd.RestDay == nil || *fwd.RestDay != 7 || fwd.MaxFocusMinPerDay == nil || *fwd.MaxFocusMinPerDay != 180 {
		t.Errorf("restDay and cap forwarded: %+v %+v", fwd.RestDay, fwd.MaxFocusMinPerDay)
	}
	if strings.Contains(string(up.lastBody), `"commitments"`) {
		t.Error("the v2 body has no legacy commitments")
	}
}

func TestSmartScheduleV2_WeekStartMustBeAMonday(t *testing.T) {
	e, up := newScheduleEnv(t)
	for _, body := range []string{`{"weekStart":"2026-11-17"}`, `{"weekStart":"2026-13-01"}`, `{"weekStart":""}`} {
		w := doRequestAs(e, http.MethodPost, "/analytics/smart-schedule", body, "")
		if w.Code != http.StatusBadRequest || codeOf(w.Body.Bytes()) != "week_start_invalid" {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if up.calls != 0 {
		t.Fatal("nothing reaches the analytics service")
	}
}

func TestSmartScheduleV2_DefaultTargetsAndLegacyBodyStillProxied(t *testing.T) {
	e, up := newScheduleEnv(t)
	doRequestAs(e, http.MethodPost, "/analytics/smart-schedule", `{"weekStart":"2026-11-16"}`, "")
	if !strings.Contains(string(up.lastBody), `"deepWorkHours":15`) || !strings.Contains(string(up.lastBody), `"restDay":null`) {
		t.Fatalf("no saved targets: the page defaults go to the generator: %s", up.lastBody)
	}
	// The previous web (flag off in its cache) still sends the legacy body during the deploy.
	w := doRequestAs(e, http.MethodPost, "/analytics/smart-schedule", validSchedule, "")
	if w.Code != http.StatusOK || !strings.Contains(string(up.lastBody), `"commitments"`) {
		t.Fatalf("legacy body: %d %s / %s", w.Code, w.Body, up.lastBody)
	}
}
