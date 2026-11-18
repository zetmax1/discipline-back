package services

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// testdata/compelling.json mirrors the Free Dictionary API response for "compelling":
// an archaic verb sense first, the adjective sense the pool intends last.
func serveFixture(t *testing.T, path string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	t.Cleanup(srv.Close)
	old := dictionaryAPIBaseURL
	dictionaryAPIBaseURL = srv.URL + "/"
	t.Cleanup(func() { dictionaryAPIBaseURL = old })
}

func TestMeaningMatchingTheIntendedPartOfSpeechIsChosen(t *testing.T) {
	serveFixture(t, "testdata/compelling.json")
	def := (&vocabService{}).fetchEnglishDefinition("compelling", "adjective")
	if def == nil {
		t.Fatal("expected a definition")
	}
	if def.PartOfSpeech != "adjective" || def.Definition != "Strongly or irresistibly evoking interest or attention." {
		t.Fatalf("must pick the adjective sense, got %q: %q", def.PartOfSpeech, def.Definition)
	}
	if def.Example != "A compelling argument." || def.Synonym != "convincing" {
		t.Fatalf("example/synonym must come from the chosen meaning, got %+v", def)
	}
}

func TestFirstMeaningIsUsedWhenNoneMatches(t *testing.T) {
	serveFixture(t, "testdata/compelling.json")
	def := (&vocabService{}).fetchEnglishDefinition("compelling", "adverb")
	if def == nil || def.PartOfSpeech != "verb" || def.Definition != "To drive together, round up" {
		t.Fatalf("expected the first meaning as fallback, got %+v", def)
	}
}

func TestPoolDeclaresAPartOfSpeechForEveryWord(t *testing.T) {
	allowed := map[string]bool{"noun": true, "verb": true, "adjective": true, "adverb": true}
	for _, w := range englishWordPool {
		if !allowed[w.PartOfSpeech] {
			t.Errorf("%s has no valid intended part of speech (%q)", w.Word, w.PartOfSpeech)
		}
	}
	for _, w := range englishWordPool {
		if w.Word == "compelling" && w.PartOfSpeech != "adjective" {
			t.Fatalf("compelling is taught as an adjective, got %q", w.PartOfSpeech)
		}
	}
	for _, w := range russianWordPool {
		if !allowed[w.PartOfSpeech] {
			t.Errorf("%s has no part of speech", w.Word)
		}
	}
}

func TestDefinitionsAreTrimmedTo300Characters(t *testing.T) {
	long := strings.Repeat("ж", 400)
	got := trimDefinition(long)
	if n := utf8.RuneCountInString(got); n != 300 {
		t.Fatalf("expected 300 characters, got %d", n)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatal("a trimmed definition should end with an ellipsis")
	}
	if trimDefinition("short") != "short" {
		t.Fatal("short definitions must be unchanged")
	}
}
