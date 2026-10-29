package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"discipline-backend/internal/services"
)

// VocabHandler handles vocabulary HTTP requests.
type VocabHandler struct {
	vocabService services.VocabService
}

// NewVocabHandler creates a new VocabHandler.
func NewVocabHandler(vocabService services.VocabService) *VocabHandler {
	return &VocabHandler{vocabService: vocabService}
}

// GetTodayWords godoc
// @Summary      Get today's vocabulary words
// @Description  Returns 2 words for today (1 English C1-C2, 1 Russian A2+). Fetches from external API on first call of the day.
// @Tags         Vocabulary
// @Produce      json
// @Security     BearerAuth
// @Param        date  query  string  false  "Date (YYYY-MM-DD), defaults to today"
// @Success      200   {object}  map[string]interface{}
// @Failure      500   {object}  ErrorResponse
// @Router       /vocabulary/today [get]
func (h *VocabHandler) GetTodayWords(c *gin.Context) {
	userID := c.MustGet("userID").(uint)
	date := c.Query("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}

	words, err := h.vocabService.GetTodayWords(userID, date)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch vocabulary"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"words": words,
		"count": len(words),
	})
}

// ToggleLearned godoc
// @Summary      Toggle word learned status
// @Description  Toggles the learned flag for a vocabulary word
// @Tags         Vocabulary
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  int  true  "Word ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      404  {object}  ErrorResponse
// @Router       /vocabulary/{id}/learned [put]
func (h *VocabHandler) ToggleLearned(c *gin.Context) {
	userID := c.MustGet("userID").(uint)
	wordID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid word ID"})
		return
	}

	word, err := h.vocabService.ToggleLearned(uint(wordID), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Word not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"word": word})
}

// ToggleSaved godoc
// @Summary      Toggle word saved status
// @Description  Toggles the saved flag for a vocabulary word
// @Tags         Vocabulary
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  int  true  "Word ID"
// @Success      200  {object}  map[string]interface{}
// @Failure      404  {object}  ErrorResponse
// @Router       /vocabulary/{id}/saved [put]
func (h *VocabHandler) ToggleSaved(c *gin.Context) {
	userID := c.MustGet("userID").(uint)
	wordID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid word ID"})
		return
	}

	word, err := h.vocabService.ToggleSaved(uint(wordID), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Word not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"word": word})
}

// GetAllWords godoc
// @Summary      Get all vocabulary words
// @Description  Returns vocabulary words for the authenticated user with pagination and optional language filter
// @Tags         Vocabulary
// @Produce      json
// @Security     BearerAuth
// @Param        lang      query  string  false  "Language filter: english or russian"
// @Param        page      query  int     false  "Page number (default 1)"
// @Param        pageSize  query  int     false  "Page size (default 30)"
// @Success      200  {object}  map[string]interface{}
// @Failure      500  {object}  ErrorResponse
// @Router       /vocabulary [get]
func (h *VocabHandler) GetAllWords(c *gin.Context) {
	userID := c.MustGet("userID").(uint)
	lang := c.Query("lang")

	page := 1
	pageSize := 30

	if p, err := strconv.Atoi(c.Query("page")); err == nil && p > 0 {
		page = p
	}
	if ps, err := strconv.Atoi(c.Query("pageSize")); err == nil && ps > 0 && ps <= 100 {
		pageSize = ps
	}

	words, total, err := h.vocabService.GetAllWords(userID, lang, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch vocabulary"})
		return
	}

	totalPages := int(total) / pageSize
	if int(total)%pageSize > 0 {
		totalPages++
	}

	c.JSON(http.StatusOK, gin.H{
		"words":      words,
		"count":      len(words),
		"total":      total,
		"page":       page,
		"pageSize":   pageSize,
		"totalPages": totalPages,
	})
}
