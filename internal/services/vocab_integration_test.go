package services

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"discipline-backend/internal/models"
	"discipline-backend/internal/repositories"
	"discipline-backend/internal/testdb"
)

// stubDictionary serves a fixed entry (after a delay, to widen race windows) or fails.
func stubDictionary(t *testing.T, fail *atomic.Bool) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`[{"word":"tenacious","meanings":[{"partOfSpeech":"adjective","definitions":[{"definition":"holding firmly"}]}]}]`))
	}))
	t.Cleanup(srv.Close)
	old := dictionaryAPIBaseURL
	dictionaryAPIBaseURL = srv.URL + "/"
	t.Cleanup(func() { dictionaryAPIBaseURL = old })
}

func newVocabEnv(t *testing.T) (*gorm.DB, VocabService, uint) {
	t.Helper()
	db, _ := testdb.NewMigrated(t)
	u := &models.User{Name: "Ann", Email: "ann@example.com", Password: "x", Role: "user"}
	if err := db.Create(u).Error; err != nil {
		t.Fatal(err)
	}
	return db, NewVocabService(repositories.NewVocabRepository(db)), u.ID
}

func countByLanguage(t *testing.T, db *gorm.DB, uid uint, date string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, lang := range []string{"english", "russian"} {
		var n int64
		db.Model(&models.VocabularyWord{}).Where("user_id = ? AND date = ? AND language = ?", uid, date, lang).Count(&n)
		out[lang] = n
	}
	return out
}

func TestTodayWords_ConcurrentCallsCreateOneRowPerLanguage(t *testing.T) {
	var fail atomic.Bool
	stubDictionary(t, &fail)
	db, svc, uid := newVocabEnv(t)

	const n = 2
	var wg sync.WaitGroup
	results := make([][]models.VocabularyWord, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = svc.GetTodayWords(uid, "2026-10-29")
		}(i)
	}
	wg.Wait()
	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("call %d: %v", i, errs[i])
		}
		if len(results[i]) != 2 {
			t.Fatalf("call %d returned %d words, want 2", i, len(results[i]))
		}
	}
	if got := countByLanguage(t, db, uid, "2026-10-29"); got["english"] != 1 || got["russian"] != 1 {
		t.Fatalf("expected exactly one row per language, got %v", got)
	}
}

func TestTodayWords_FallbackIsReturnedButNotSaved(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	stubDictionary(t, &fail)
	db, svc, uid := newVocabEnv(t)

	words, err := svc.GetTodayWords(uid, "2026-10-29")
	if err != nil {
		t.Fatal(err)
	}
	var fallback *models.VocabularyWord
	for i := range words {
		if words[i].Language == "english" {
			fallback = &words[i]
		}
	}
	if fallback == nil || fallback.ID != 0 {
		t.Fatalf("the English fallback must be returned unsaved (id 0), got %+v", words)
	}
	if got := countByLanguage(t, db, uid, "2026-10-29"); got["english"] != 0 || got["russian"] != 1 {
		t.Fatalf("fallback text must never be stored, got %v", got)
	}

	fail.Store(false)
	words, err = svc.GetTodayWords(uid, "2026-10-29")
	if err != nil {
		t.Fatal(err)
	}
	if got := countByLanguage(t, db, uid, "2026-10-29"); got["english"] != 1 {
		t.Fatalf("the next request must retry and store the real word, got %v", got)
	}
	for _, w := range words {
		if w.Language == "english" && (w.ID == 0 || w.Definition != "holding firmly") {
			t.Fatalf("expected the stored dictionary definition, got %+v", w)
		}
	}
}
