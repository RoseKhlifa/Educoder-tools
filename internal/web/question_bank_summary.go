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
	return c.loadAtLeast(ctx, bank, 0)
}

func (c *questionBankSummaryCache) loadAtLeast(ctx context.Context, bank *questionbank.Store, minimum int64) (questionbank.Summary, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bank == bank && time.Now().Before(c.expires) && c.value.TotalVerifiedQuestions >= minimum {
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
	session, ok := s.sessionFromRequest(r)
	if !ok {
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
	// The shared cache contains only global counts. Personal contribution data
	// is derived from this request's authenticated account and never cached in
	// that value, regardless of which user primed the global summary.
	contributed, err := s.jobs.bank.ContributedQuestions(ctx, sessionJobOwner(session))
	if err != nil {
		s.writeError(w, requestID, http.StatusServiceUnavailable, "QUESTION_BANK_UNAVAILABLE", "题库概况暂不可用，请稍后重试")
		return
	}
	// A collection can grow the bank while the shared minute cache is warm.
	// Refresh in that case instead of returning impossible personal/global data.
	if contributed > value.TotalVerifiedQuestions {
		value, err = s.questionBankSummary.loadAtLeast(ctx, s.jobs.bank, contributed)
		if err != nil {
			s.writeError(w, requestID, http.StatusServiceUnavailable, "QUESTION_BANK_UNAVAILABLE", "题库概况暂不可用，请稍后重试")
			return
		}
	}
	s.writeJSON(w, requestID, http.StatusOK, struct {
		questionbank.Summary
		ContributedQuestions int64 `json:"contributed_questions"`
	}{value, contributed})
}
