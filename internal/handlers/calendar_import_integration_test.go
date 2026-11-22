package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"discipline-backend/internal/calendarcrypt"
	"discipline-backend/internal/calendarfetch"
	"discipline-backend/internal/contracttest"
	"discipline-backend/internal/events"
	"discipline-backend/internal/ratelimit"
	"discipline-backend/internal/repositories"
	"discipline-backend/internal/services"
)

// fakeFetcher stands in for the SSRF-safe fetcher: no network in tests.
type fakeFetcher struct {
	body  string
	err   error
	calls int
}

func (f *fakeFetcher) Fetch(_ context.Context, raw string) ([]byte, string, error) {
	f.calls++
	u, err := calendarfetch.Normalize(raw)
	if err != nil {
		return nil, "", err
	}
	if f.err != nil {
		return nil, u.Hostname(), f.err
	}
	return []byte(f.body), u.Hostname(), nil
}

type failingStore struct{ ratelimit.Store }

func (failingStore) Hit(context.Context, string, int, time.Duration) (bool, time.Duration, error) {
	return false, 0, errors.New("redis down")
}

func newCalendarEnv(t *testing.T, fetch *fakeFetcher, store ratelimit.Store) *taskEnv {
	t.Helper()
	e := newCommitmentEnv(t)
	e.tz = "Asia/Tashkent"
	box, _ := calendarcrypt.New(bytes.Repeat([]byte{3}, 32))
	svc := services.NewCalendarService(repositories.NewCalendarRepository(e.db), fetch, box, events.NewRecorder(e.db))
	RegisterCalendarRoutes(e.r, NewCalendarHandler(svc, store))
	nowFunc = func() time.Time { return time.Date(2027, 3, 1, 9, 0, 0, 0, time.UTC) } // Monday 1 March 2027, 14:00 in Tashkent
	t.Cleanup(func() { nowFunc = time.Now })
	return e
}

func ics(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../icsimport/testdata/ics/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func uploadPreview(t *testing.T, e *taskEnv, body string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "my-calendar.ics")
	_, _ = fw.Write([]byte(body))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/calendar-import/preview", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

type preview struct {
	Source *struct {
		ID   *int
		Host string
	}
	Candidates []struct {
		Key, Name, Status string
		Weekdays          []int
		StartMin, EndMin  int
		ValidFrom         string
		ValidTo           *string
		CommitmentID      *int
	}
	OneOffs []struct{ Key, Name, Date, Status string }
	Removed []struct {
		CommitmentID int
		Name         string
	}
	Skipped map[string]int
}

func decodePreview(t *testing.T, w *httptest.ResponseRecorder) preview {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("previews are never cached")
	}
	contracttest.Validate(t, "calendar-import", "import_preview", w.Body.Bytes())
	var p preview
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	return p
}

// commitAll ticks every candidate (as the preview pre-checks them) with the given origin and source.
func commitAll(t *testing.T, e *taskEnv, p preview, extra string) map[string]any {
	t.Helper()
	var items []string
	for _, c := range p.Candidates {
		to := "null"
		if c.ValidTo != nil {
			to = `"` + *c.ValidTo + `"`
		}
		wd, _ := json.Marshal(c.Weekdays)
		items = append(items, fmt.Sprintf(`{"key":"%s","name":%q,"kind":"study","weekdays":%s,"startMin":%d,"endMin":%d,"validFrom":"%s","validTo":%s}`,
			c.Key, c.Name, wd, c.StartMin, c.EndMin, c.ValidFrom, to))
	}
	w := doRequestAs(e, http.MethodPost, "/calendar-import/commit", fmt.Sprintf(`{%s"items":[%s]}`, extra, strings.Join(items, ",")), "")
	if w.Code != http.StatusOK {
		t.Fatalf("commit: %d %s", w.Code, w.Body)
	}
	contracttest.Validate(t, "calendar-import", "import_result", w.Body.Bytes())
	var r map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &r)
	return r
}

func TestCalendarImport_FilePreviewCommitAndRepeat(t *testing.T) {
	e := newCalendarEnv(t, &fakeFetcher{}, ratelimit.NewMemoryStore())
	p := decodePreview(t, uploadPreview(t, e, ics(t, "google-berlin-dst.ics")))
	if p.Source != nil || len(p.Candidates) != 5 || p.Candidates[0].Status != "new" || p.Candidates[0].StartMin != 840 {
		t.Fatalf("preview: %+v", p)
	}
	if n := countRows(t, e, `SELECT count(*) FROM commitments`); n != 0 {
		t.Fatal("a preview saves nothing")
	}
	r := commitAll(t, e, p, `"origin":"file",`)
	if r["created"].(float64) != 5 || r["sourceId"] != nil {
		t.Fatalf("commit: %v", r)
	}
	list := doRequestAs(e, http.MethodGet, "/commitments", "", "").Body.String()
	if !strings.Contains(list, `"source":"ics_file"`) || !strings.Contains(list, `"name":"Choir"`) {
		t.Fatalf("list: %s", list)
	}
	// The same file again: unchanged, linked to the saved commitments; an app edit shows as such.
	p2 := decodePreview(t, uploadPreview(t, e, ics(t, "google-berlin-dst.ics")))
	if p2.Candidates[0].Status != "unchanged" || p2.Candidates[0].CommitmentID == nil {
		t.Fatalf("repeat: %+v", p2.Candidates[0])
	}
	id := *p2.Candidates[1].CommitmentID
	mustExecDB(t, e, `UPDATE commitments SET name = 'My lecture', updated_at = now() + interval '1 minute' WHERE id = ?`, id)
	p3 := decodePreview(t, uploadPreview(t, e, ics(t, "google-berlin-dst.ics")))
	if p3.Candidates[1].Status != "edited_in_app" {
		t.Fatalf("edited in the app: %+v", p3.Candidates[1])
	}
	// Committing the same keys again upserts: no duplicates.
	commitAll(t, e, p, `"origin":"file",`)
	if n := countRows(t, e, `SELECT count(*) FROM commitments`); n != 5 {
		t.Fatalf("upsert by key, got %d rows", n)
	}
	var props string
	e.db.Raw(`SELECT props::text FROM product_events WHERE name = 'calendar_imported' ORDER BY id LIMIT 1`).Scan(&props)
	if !strings.Contains(props, `"origin": "file"`) || !strings.Contains(props, `"created": 5`) || strings.Contains(props, "Choir") {
		t.Fatalf("calendar_imported props: %s", props)
	}
}

func TestCalendarImport_LinkSourceRefreshAndRemove(t *testing.T) {
	fetch := &fakeFetcher{body: ics(t, "google-berlin-dst.ics")}
	e := newCalendarEnv(t, fetch, ratelimit.NewMemoryStore())
	secret := "https://calendar.google.com/calendar/ical/me/private-SECRET/basic.ics"
	p := decodePreview(t, doRequestAs(e, http.MethodPost, "/calendar-import/preview", `{"url":"`+secret+`"}`, ""))
	if p.Source == nil || p.Source.Host != "calendar.google.com" || p.Source.ID != nil {
		t.Fatalf("source: %+v", p.Source)
	}
	r := commitAll(t, e, p, `"origin":"url","url":"`+secret+`",`)
	srcID := int(r["sourceId"].(float64))
	var cipher []byte
	e.db.Raw(`SELECT url_ciphertext FROM calendar_sources WHERE id = ?`, srcID).Scan(&cipher)
	if bytes.Contains(cipher, []byte("SECRET")) {
		t.Fatal("the URL is stored encrypted")
	}
	w := doRequestAs(e, http.MethodGet, "/calendar-sources", "", "")
	contracttest.Validate(t, "calendar-import", "source_list", w.Body.Bytes())
	if strings.Contains(w.Body.String(), "SECRET") || !strings.Contains(w.Body.String(), `"commitmentCount":5`) {
		t.Fatalf("sources: %s", w.Body)
	}
	// Refresh: the calendar lost the choir and changed the lecture's end.
	fetch.body = strings.Replace(strings.Replace(ics(t, "google-berlin-dst.ics"), "20270301T113000", "20270301T120000", 1),
		"RRULE:FREQ=WEEKLY;BYDAY=TU\r\n", "RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=1\r\n", 1)
	if !strings.Contains(fetch.body, "COUNT=1") {
		fetch.body = strings.Replace(fetch.body, "RRULE:FREQ=WEEKLY;BYDAY=TU\n", "RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=1\n", 1)
	}
	p2 := decodePreview(t, doRequestAs(e, http.MethodPost, "/calendar-import/preview", fmt.Sprintf(`{"sourceId":%d}`, srcID), ""))
	statuses := map[string]int{}
	for _, c := range p2.Candidates {
		statuses[c.Status]++
	}
	if statuses["changed"] != 3 || len(p2.Removed) != 2 { // two lecture runs + the choir's first run; its later runs are gone
		t.Fatalf("refresh: %+v removed %+v", statuses, p2.Removed)
	}
	removeIDs, _ := json.Marshal(func() []int {
		var ids []int
		for _, r := range p2.Removed {
			ids = append(ids, r.CommitmentID)
		}
		return ids
	}())
	r2 := commitAll(t, e, p2, fmt.Sprintf(`"origin":"url","sourceId":%d,"removeIds":%s,`, srcID, removeIDs))
	if r2["removed"].(float64) < 1 || r2["updated"].(float64) < 2 {
		t.Fatalf("refresh commit: %v", r2)
	}
	// Remove the source but keep its commitments (they become manual ones).
	if w := doRequestAs(e, http.MethodDelete, fmt.Sprintf("/calendar-sources/%d", srcID), "", ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete source: %d %s", w.Code, w.Body)
	}
	if n := countRows(t, e, `SELECT count(*) FROM commitments WHERE calendar_source_id IS NULL`); n == 0 {
		t.Fatal("kept commitments lose their source")
	}
	if n := countRows(t, e, `SELECT count(*) FROM calendar_sources`); n != 0 {
		t.Fatal("source deleted")
	}
}

func TestCalendarImport_ErrorsLimitsAndAuthorization(t *testing.T) {
	fetch := &fakeFetcher{body: ics(t, "misc.ics")}
	e := newCalendarEnv(t, fetch, ratelimit.NewMemoryStore())
	for body, code := range map[string]string{
		`{"url":"https://127.0.0.1/c.ics"}`:           "ics_url_blocked",
		`{"url":"http://calendar.example.com/c.ics"}`: "ics_url_invalid",
		`{"sourceId":999}`:                            "calendar_source_not_found",
		`{}`:                                          "validation_failed",
	} {
		w := doRequestAs(e, http.MethodPost, "/calendar-import/preview", body, "")
		if codeOf(w.Body.Bytes()) != code || strings.Contains(w.Body.String(), "127.0.0.1") {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if w := uploadPreview(t, e, "not a calendar"); codeOf(w.Body.Bytes()) != "ics_invalid" {
		t.Errorf("bad file: %d %s", w.Code, w.Body)
	}
	fetch.err = calendarfetch.ErrTimeout
	if w := doRequestAs(e, http.MethodPost, "/calendar-import/preview", `{"url":"https://example.com/c.ics"}`, ""); codeOf(w.Body.Bytes()) != "ics_fetch_timeout" {
		t.Errorf("timeout: %d %s", w.Code, w.Body)
	}
	fetch.err = nil
	// At most 5 linked calendars.
	for i := 0; i < 5; i++ {
		mustExecDB(t, e, `INSERT INTO calendar_sources (user_id, url_ciphertext, url_fingerprint, url_host) VALUES (?, '\x00', ?, 'h.example.com')`, e.ids[0], []byte{byte(i)})
	}
	w := doRequestAs(e, http.MethodPost, "/calendar-import/commit", `{"origin":"url","url":"https://new.example.com/c.ics","items":[]}`, "")
	if codeOf(w.Body.Bytes()) != "calendar_source_limit" {
		t.Errorf("6th source: %d %s", w.Code, w.Body)
	}
	// Another user's source is not theirs: 404 on refresh, delete and commit.
	var other int
	e.db.Raw(`SELECT id FROM calendar_sources LIMIT 1`).Scan(&other)
	for _, req := range [][3]string{
		{http.MethodPost, "/calendar-import/preview", fmt.Sprintf(`{"sourceId":%d}`, other)},
		{http.MethodDelete, fmt.Sprintf("/calendar-sources/%d", other), ""},
		{http.MethodPost, "/calendar-import/commit", fmt.Sprintf(`{"origin":"url","sourceId":%d,"items":[]}`, other)},
	} {
		if w := doRequestAs(e, req[0], req[1], req[2], "2"); w.Code != http.StatusNotFound || codeOf(w.Body.Bytes()) != "calendar_source_not_found" {
			t.Errorf("%v as another user: %d %s", req, w.Code, w.Body)
		}
	}
}

func TestCalendarImport_FetchLimitFailsClosedAndLogsNothingPersonal(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	fetch := &fakeFetcher{body: ics(t, "google-berlin-dst.ics")}
	e := newCalendarEnv(t, fetch, failingStore{})
	w := doRequestAs(e, http.MethodPost, "/calendar-import/preview", `{"url":"https://calendar.google.com/private-SECRET/basic.ics"}`, "")
	if w.Code != http.StatusServiceUnavailable || fetch.calls != 0 {
		t.Fatalf("outbound fetches fail closed without the rate-limit store: %d, %d calls", w.Code, fetch.calls)
	}
	// A file preview fails open (no outbound request).
	p := decodePreview(t, uploadPreview(t, e, ics(t, "google-berlin-dst.ics")))
	commitAll(t, e, p, `"origin":"file",`)
	for _, leak := range []string{"SECRET", "private", "Statistics", "Choir", "lecture-stats", "my-calendar"} {
		if strings.Contains(logs.String(), leak) {
			t.Fatalf("logs leak %q: %s", leak, logs.String())
		}
	}
}

func TestCalendarImport_FetchRateLimit(t *testing.T) {
	fetch := &fakeFetcher{body: ics(t, "misc.ics")}
	e := newCalendarEnv(t, fetch, ratelimit.NewMemoryStore())
	var last int
	for i := 0; i < 6; i++ {
		last = doRequestAs(e, http.MethodPost, "/calendar-import/preview", `{"url":"https://example.com/c.ics"}`, "").Code
	}
	if last != http.StatusTooManyRequests || fetch.calls != 5 {
		t.Fatalf("5 fetches a minute: last %d after %d fetches", last, fetch.calls)
	}
}
