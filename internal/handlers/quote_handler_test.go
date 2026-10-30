package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubQuoteAPI serves body as the quote API's response for the test's duration.
func stubQuoteAPI(t *testing.T, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	old := quoteAPIURL
	quoteAPIURL = srv.URL
	t.Cleanup(func() { quoteAPIURL = old })
}

const apiQuote = `[{"q":"From the API.","a":"Stub"}]`

func TestFetchQuote_UsesTheAPIQuote(t *testing.T) {
	stubQuoteAPI(t, apiQuote)
	got := NewQuoteHandler().fetchQuote()
	if got.Quote != "From the API." || got.Author != "Stub" {
		t.Fatalf("want the API's quote, got %+v", got)
	}
}

func TestFetchQuote_OversizedResponseFallsBack(t *testing.T) {
	// Valid JSON, but padded past the limit: an unbounded read would accept it.
	stubQuoteAPI(t, apiQuote+strings.Repeat(" ", maxQuoteResponseBytes))
	got := NewQuoteHandler().fetchQuote()
	if got.Quote == "From the API." {
		t.Fatalf("a response over %d bytes must not be used, got %+v", maxQuoteResponseBytes, got)
	}
}
