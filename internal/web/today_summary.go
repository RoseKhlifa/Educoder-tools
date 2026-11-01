package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"educoder-cli/internal/platform"
	"educoder-cli/internal/questionbank"
)

// Cache only the anonymous aggregate, never a principal or a question. Recheck
// authentication for every request and expire immediately at Shanghai midnight.
type todaySummaryCache struct {
	mu       sync.Mutex
	bank     *questionbank.Store
	platform *platform.Store
	value    questionbank.TodaySummary
	expires  time.Time
}

func (c *todaySummaryCache) load(ctx context.Context, bank *questionbank.Store, accounts *platform.Store, now time.Time) (questionbank.TodaySummary, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return questionbank.TodaySummary{}, err
	}
	day := now.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02")
	if c.bank == bank && c.platform == accounts && c.value.Date == day && now.Before(c.expires) {
		return c.value, nil
	}
	owners, err := accounts.VerifiedJobOwners(ctx)
	if err != nil {
		return questionbank.TodaySummary{}, err
	}
	linked := make(map[string]struct{}, len(owners))
	for _, owner := range owners {
		sum := sha256.Sum256([]byte(owner))
		linked[hex.EncodeToString(sum[:])] = struct{}{}
	}
	value, err := bank.TodaySummary(ctx, now, linked)
	if err != nil {
		return questionbank.TodaySummary{}, err
	}
	c.bank, c.platform, c.value, c.expires = bank, accounts, value, now.Add(time.Minute)
	return value, nil
}

func (s *Server) handleTodaySummary(w http.ResponseWriter, r *http.Request, requestID string) {
	if _, ok := s.sessionFromRequest(r); !ok {
		s.authError(w, requestID)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.methodError(w, requestID)
		return
	}
	unavailable := func() {
		s.writeError(w, requestID, http.StatusServiceUnavailable, "SERVICE_SUMMARY_UNAVAILABLE", "今日服务概况暂不可用，请稍后重试")
	}
	if s.jobs == nil || s.jobs.bank == nil || s.platform == nil {
		unavailable()
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	value, err := s.todaySummary.load(ctx, s.jobs.bank, s.platform, time.Now())
	if err != nil {
		unavailable()
		return
	}
	s.writeJSON(w, requestID, http.StatusOK, value)
}
