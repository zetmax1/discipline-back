package services

import (
	"encoding/json"
	"io"
	"log"
	"math/rand"
	"net/http"
	"time"

	"discipline-backend/internal/models"
	"discipline-backend/internal/repositories"
)

// VocabService defines vocabulary business logic.
type VocabService interface {
	GetTodayWords(userID uint, date string) ([]models.VocabularyWord, error)
	ToggleLearned(id, userID uint) (*models.VocabularyWord, error)
	ToggleSaved(id, userID uint) (*models.VocabularyWord, error)
	GetAllWords(userID uint, lang string, page, pageSize int) ([]models.VocabularyWord, int64, error)
}

type vocabService struct {
	vocabRepo repositories.VocabRepository
}

// NewVocabService creates a new VocabService.
func NewVocabService(vocabRepo repositories.VocabRepository) VocabService {
	return &vocabService{vocabRepo: vocabRepo}
}

// GetTodayWords returns today's vocabulary words. Fetches from external API if not cached.
func (s *vocabService) GetTodayWords(userID uint, date string) ([]models.VocabularyWord, error) {
	words, err := s.vocabRepo.FindByUserAndDate(userID, date)
	if err != nil {
		return nil, err
	}

	// Check if we have at least one word per language
	hasEnglish := false
	hasRussian := false
	for _, w := range words {
		if w.Language == "english" {
			hasEnglish = true
		}
		if w.Language == "russian" {
			hasRussian = true
		}
	}

	// Only fetch if missing a language
	if !hasEnglish || !hasRussian {
		newWords, unsaved := s.fetchDailyWords(userID, date)
		for i := range newWords {
			// Skip if we already have this language
			if newWords[i].Language == "english" && hasEnglish {
				continue
			}
			if newWords[i].Language == "russian" && hasRussian {
				continue
			}
			// A concurrent request may have inserted the same word; ON CONFLICT keeps one row.
			if err := s.vocabRepo.CreateIfAbsent(&newWords[i]); err != nil {
				return nil, err
			}
		}
		// Re-fetch to include newly created words
		words, err := s.vocabRepo.FindByUserAndDate(userID, date)
		if err != nil {
			return nil, err
		}
		// The fallback is returned but never stored, so the next request retries the dictionary.
		for _, w := range unsaved {
			if !hasLanguage(words, w.Language) {
				words = append(words, w)
			}
		}
		return words, nil
	}

	return words, nil
}

func hasLanguage(words []models.VocabularyWord, lang string) bool {
	for _, w := range words {
		if w.Language == lang {
			return true
		}
	}
	return false
}

func (s *vocabService) ToggleLearned(id, userID uint) (*models.VocabularyWord, error) {
	word, err := s.vocabRepo.FindByID(id, userID)
	if err != nil {
		return nil, err
	}
	word.Learned = !word.Learned
	if err := s.vocabRepo.Update(word); err != nil {
		return nil, err
	}
	return word, nil
}

func (s *vocabService) ToggleSaved(id, userID uint) (*models.VocabularyWord, error) {
	word, err := s.vocabRepo.FindByID(id, userID)
	if err != nil {
		return nil, err
	}
	word.Saved = !word.Saved
	if err := s.vocabRepo.Update(word); err != nil {
		return nil, err
	}
	return word, nil
}

func (s *vocabService) GetAllWords(userID uint, lang string, page, pageSize int) ([]models.VocabularyWord, int64, error) {
	return s.vocabRepo.FindAllByUserPaginated(userID, lang, page, pageSize)
}

// --- Russian word pool with built-in definitions ---

type russianEntry struct {
	Word       string
	Definition string
	Example    string
}

var russianWordPool = []russianEntry{
	{"достижение", "то, чего добились усилиями; успех, результат", "Это было большое достижение в его карьере."},
	{"развитие", "процесс перехода к более совершенному состоянию", "Развитие навыков требует постоянной практики."},
	{"стремление", "сильное желание достичь чего-либо", "Стремление к знаниям — основа прогресса."},
	{"настойчивость", "упорство в достижении цели", "Настойчивость помогает преодолевать трудности."},
	{"усилие", "напряжение физических или умственных сил", "Каждое усилие приближает к цели."},
	{"способность", "умение, возможность что-либо делать", "Способность учиться — важное качество."},
	{"уверенность", "твёрдая вера в свои силы", "Уверенность приходит с опытом."},
	{"ответственность", "обязанность отвечать за свои действия", "Ответственность — признак зрелости."},
	{"дисциплина", "подчинение правилам и порядку", "Самодисциплина ведёт к успеху."},
	{"привычка", "действие, ставшее потребностью", "Хорошие привычки формируются за 21 день."},
	{"решение", "заключение, вывод о необходимости действий", "Он принял решение изменить свою жизнь."},
	{"терпение", "способность спокойно ожидать и переносить трудности", "Терпение — ключ к мастерству."},
	{"внимание", "сосредоточенность на чём-либо", "Внимание к деталям имеет значение."},
	{"возможность", "благоприятное условие для чего-либо", "Каждый день открывает новые возможности."},
	{"сосредоточенность", "полная концентрация на задаче", "Сосредоточенность повышает продуктивность."},
	{"целеустремлённость", "упорное стремление к достижению цели", "Целеустремлённость — ключевое качество лидера."},
	{"продуктивность", "способность создавать результаты эффективно", "Продуктивность зависит от планирования."},
	{"мотивация", "побуждение к деятельности", "Внутренняя мотивация сильнее внешней."},
	{"опыт", "знания, полученные на практике", "Опыт — лучший учитель."},
	{"планирование", "составление плана действий", "Планирование экономит время и силы."},
	{"последовательность", "действие за действием без пропусков", "Последовательность важнее интенсивности."},
	{"результат", "итог деятельности или процесса", "Результат зависит от усилий."},
	{"трудолюбие", "любовь к труду, усердие", "Трудолюбие ценится в любой профессии."},
	{"прогресс", "движение вперёд, улучшение", "Даже маленький прогресс — это прогресс."},
	{"самосовершенствование", "работа над улучшением своих качеств", "Самосовершенствование — путь всей жизни."},
	{"выносливость", "способность выдерживать длительную нагрузку", "Выносливость развивается постепенно."},
	{"целеполагание", "процесс постановки целей", "Целеполагание — первый шаг к успеху."},
	{"осознанность", "состояние полного присутствия в моменте", "Осознанность помогает принимать лучшие решения."},
	{"сила воли", "способность управлять своими действиями", "Сила воли тренируется как мышца."},
	{"эффективность", "достижение результата с минимумом затрат", "Эффективность — не скорость, а качество."},
}

// --- Fetching ---

// fetchDailyWords returns the day's words to store and, if the dictionary lookup
// failed, an unsaved English fallback (ID 0) to show this time only.
func (s *vocabService) fetchDailyWords(userID uint, date string) (words, unsaved []models.VocabularyWord) {

	// Pick deterministically based on date
	dayHash := hashDate(date)

	// English: fetch from API (with part of speech)
	englishWords := []string{
		"resilience", "pragmatic", "eloquent", "meticulous", "tenacious",
		"paradigm", "ubiquitous", "ephemeral", "ambiguous", "paramount",
		"diligent", "profound", "articulate", "perseverance", "scrutinize",
		"deliberate", "autonomous", "intrinsic", "cumulative", "discrepancy",
		"nuance", "coherent", "substantiate", "implications", "disposition",
		"compelling", "comprehensive", "inadvertent", "discernible", "exemplary",
	}

	enWord := englishWords[dayHash%len(englishWords)]
	if enDef := s.fetchEnglishDefinition(enWord); enDef != nil && enDef.Definition != "" {
		words = append(words, models.VocabularyWord{
			UserID:       userID,
			Word:         enDef.Word,
			Language:     "english",
			Definition:   enDef.Definition,
			Example:      enDef.Example,
			Synonym:      enDef.Synonym,
			PartOfSpeech: enDef.PartOfSpeech,
			Date:         date,
		})
	} else {
		// English fallback: never persisted
		unsaved = append(unsaved, models.VocabularyWord{
			UserID:     userID,
			Word:       enWord,
			Language:   "english",
			Definition: "An advanced English vocabulary word",
			Date:       date,
		})
	}

	// Russian: use built-in definitions (API doesn't support Russian well)
	ruEntry := russianWordPool[dayHash%len(russianWordPool)]
	words = append(words, models.VocabularyWord{
		UserID:     userID,
		Word:       ruEntry.Word,
		Language:   "russian",
		Definition: ruEntry.Definition,
		Example:    ruEntry.Example,
		Date:       date,
	})

	return words, unsaved
}

// dictionaryAPIBaseURL is the Free Dictionary API entry endpoint (overridden in tests).
var dictionaryAPIBaseURL = "https://api.dictionaryapi.dev/api/v2/entries/en/"

// maxDictionaryResponseBytes caps how much of a dictionary response is read.
const maxDictionaryResponseBytes = 1 << 20

type wordDefinition struct {
	Word         string
	Definition   string
	Example      string
	Synonym      string
	PartOfSpeech string
}

// fetchEnglishDefinition calls the Free Dictionary API and extracts part of speech.
func (s *vocabService) fetchEnglishDefinition(word string) *wordDefinition {
	client := &http.Client{Timeout: 5 * time.Second}
	url := dictionaryAPIBaseURL + word

	resp, err := client.Get(url)
	if err != nil {
		log.Printf("Dictionary API error for %s: %v", word, err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Printf("Dictionary API returned %d for %s", resp.StatusCode, word)
		return nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxDictionaryResponseBytes+1))
	if err != nil {
		return nil
	}
	if len(body) > maxDictionaryResponseBytes {
		log.Printf("Dictionary API response for %s exceeded %d bytes", word, maxDictionaryResponseBytes)
		return nil
	}

	var entries []struct {
		Word     string `json:"word"`
		Meanings []struct {
			PartOfSpeech string `json:"partOfSpeech"`
			Definitions  []struct {
				Definition string `json:"definition"`
				Example    string `json:"example"`
			} `json:"definitions"`
			Synonyms []string `json:"synonyms"`
		} `json:"meanings"`
	}

	if err := json.Unmarshal(body, &entries); err != nil || len(entries) == 0 {
		return nil
	}

	entry := entries[0]
	def := &wordDefinition{Word: entry.Word}

	if len(entry.Meanings) > 0 {
		meaning := entry.Meanings[0]
		def.PartOfSpeech = meaning.PartOfSpeech
		if len(meaning.Definitions) > 0 {
			def.Definition = meaning.Definitions[0].Definition
			def.Example = meaning.Definitions[0].Example
		}
		if len(meaning.Synonyms) > 0 {
			def.Synonym = meaning.Synonyms[0]
		}
	}

	return def
}

// hashDate creates a stable integer from a YYYY-MM-DD string.
func hashDate(date string) int {
	h := 0
	for _, c := range date {
		h = h*31 + int(c)
	}
	if h < 0 {
		h = -h
	}
	r := rand.New(rand.NewSource(int64(h)))
	return r.Intn(10000)
}
