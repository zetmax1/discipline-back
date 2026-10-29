package services

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchEnglishDefinition_RejectsOversizeResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"word":"x","meanings":[{"partOfSpeech":"noun","definitions":[{"definition":"` +
			strings.Repeat("a", maxDictionaryResponseBytes) + `"}]}]}]`))
	}))
	t.Cleanup(srv.Close)

	old := dictionaryAPIBaseURL
	dictionaryAPIBaseURL = srv.URL + "/"
	t.Cleanup(func() { dictionaryAPIBaseURL = old })

	if def := (&vocabService{}).fetchEnglishDefinition("x"); def != nil {
		t.Fatal("an oversize dictionary response must be rejected")
	}
}

func TestFetchEnglishDefinition_ParsesNormalResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"word":"x","meanings":[{"partOfSpeech":"noun","definitions":[{"definition":"a thing"}]}]}]`))
	}))
	t.Cleanup(srv.Close)

	old := dictionaryAPIBaseURL
	dictionaryAPIBaseURL = srv.URL + "/"
	t.Cleanup(func() { dictionaryAPIBaseURL = old })

	def := (&vocabService{}).fetchEnglishDefinition("x")
	if def == nil || def.Definition != "a thing" {
		t.Fatalf("expected parsed definition, got %+v", def)
	}
}
