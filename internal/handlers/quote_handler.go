package handlers

import (
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// QuoteHandler handles motivational quote requests.
type QuoteHandler struct {
	mu        sync.RWMutex
	quote     *CachedQuote
	cacheTime time.Time
	cacheTTL  time.Duration
}

// CachedQuote holds a cached quote.
type CachedQuote struct {
	Quote  string `json:"quote" example:"Discipline is the bridge between goals and accomplishment."`
	Author string `json:"author" example:"Jim Rohn"`
}

// NewQuoteHandler creates a new QuoteHandler with a 2-hour cache TTL.
func NewQuoteHandler() *QuoteHandler {
	return &QuoteHandler{
		cacheTTL: 2*time.Hour + time.Duration(rand.Intn(60))*time.Minute, // 2-3 hours
	}
}

// fallbackQuotes are used when the external API is unreachable.
var fallbackQuotes = []CachedQuote{
	{Quote: "Discipline is the bridge between goals and accomplishment.", Author: "Jim Rohn"},
	{Quote: "We are what we repeatedly do. Excellence is not an act, but a habit.", Author: "Aristotle"},
	{Quote: "The secret of getting ahead is getting started.", Author: "Mark Twain"},
	{Quote: "Small daily improvements are the key to staggering long-term results.", Author: "Robin Sharma"},
	{Quote: "Focus on being productive instead of busy.", Author: "Tim Ferriss"},
	{Quote: "It's not about having time, it's about making time.", Author: "Unknown"},
	{Quote: "Success is the sum of small efforts repeated day in and day out.", Author: "Robert Collier"},
	{Quote: "Don't watch the clock; do what it does. Keep going.", Author: "Sam Levenson"},
	{Quote: "The only way to do great work is to love what you do.", Author: "Steve Jobs"},
	{Quote: "Motivation gets you going, but discipline keeps you growing.", Author: "John C. Maxwell"},
}

// GetQuote godoc
// @Summary      Get motivational quote
// @Description  Returns a motivational quote. Cached for 2-3 hours, refreshes automatically.
// @Tags         Quotes
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  CachedQuote
// @Router       /quote [get]
func (h *QuoteHandler) GetQuote(c *gin.Context) {
	h.mu.RLock()
	if h.quote != nil && time.Since(h.cacheTime) < h.cacheTTL {
		quote := *h.quote
		h.mu.RUnlock()
		c.JSON(http.StatusOK, quote)
		return
	}
	h.mu.RUnlock()

	// Fetch new quote
	quote := h.fetchQuote()
	c.JSON(http.StatusOK, quote)
}

// fetchQuote gets a quote from ZenQuotes API or falls back to local list.
func (h *QuoteHandler) fetchQuote() CachedQuote {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://zenquotes.io/api/random")
	if err != nil {
		return h.useFallback()
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return h.useFallback()
	}

	// ZenQuotes returns an array: [{"q":"...", "a":"...", "h":"..."}]
	var zenQuotes []struct {
		Q string `json:"q"`
		A string `json:"a"`
	}
	if err := json.Unmarshal(body, &zenQuotes); err != nil || len(zenQuotes) == 0 {
		return h.useFallback()
	}

	quote := CachedQuote{
		Quote:  zenQuotes[0].Q,
		Author: zenQuotes[0].A,
	}

	h.mu.Lock()
	h.quote = &quote
	h.cacheTime = time.Now()
	h.cacheTTL = 2*time.Hour + time.Duration(rand.Intn(60))*time.Minute
	h.mu.Unlock()

	return quote
}

func (h *QuoteHandler) useFallback() CachedQuote {
	quote := fallbackQuotes[rand.Intn(len(fallbackQuotes))]

	h.mu.Lock()
	h.quote = &quote
	h.cacheTime = time.Now()
	h.mu.Unlock()

	return quote
}
