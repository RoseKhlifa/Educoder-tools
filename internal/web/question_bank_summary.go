package web

import (
	"context"
	"net/http"
	"sync"
	"time"

	"educoder-cli/internal/questionbank"
)

// A sidebar poll never rescans the shared bank more than once a minute. The
// mutex also coalesces concurrent requests; cached data contains no user data.
// Authentication is checked on every request, including cache hits.
type questionBankSummaryCache struct {
	mu      sync.Mutex
	bank    *questionbank.Store
	value   questionbank.Summary
	expires time.Time
}

func (c *questionBankSummaryCache) load(ctx context.Context, bank *questionbank.Store) (questionbank.Summary, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bank == bank && time.Now().Before(c.expires) {
		return c.value, nil
	}
	value, err := bank.Summary(ctx)
	if err != nil {
		return questionbank.Summary{}, err
	}
	c.bank, c.value, c.expires = bank, value, time.Now().Add(time.Minute)
	return value, nil
}

func (s *Server) handleQuestionBankSummary(w http.ResponseWriter, r *http.Request, requestID string) {
	if _, ok := s.sessionFromRequest(r); !ok {
		s.authError(w, requestID)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.methodError(w, requestID)
		return
	}
	if s.jobs == nil || s.jobs.bank == nil {
		s.writeError(w, requestID, http.StatusServiceUnavailable, "QUESTION_BANK_UNAVAILABLE", "题库概况暂不可用，请稍后重试")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	value, err := s.questionBankSummary.load(ctx, s.jobs.bank)
	if err != nil {
		s.writeError(w, requestID, http.StatusServiceUnavailable, "QUESTION_BANK_UNAVAILABLE", "题库概况暂不可用，请稍后重试")
		return
	}
	s.writeJSON(w, requestID, http.StatusOK, value)
}
