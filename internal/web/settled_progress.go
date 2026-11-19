package web

import (
	"sync"
	"time"

	"educoder-cli/internal/educoder"
)

// settledProgressTTL bounds how long a proven "nothing left to do" state is
// reused for the course list. Full scores do not regress in normal use, so the
// only staleness is a teacher reset or requirement change inside this window.
const settledProgressTTL = 30 * time.Minute

const settledProgressMaxEntries = 50000

// settledProgressCache remembers, per account and assignment, that a complete
// progress read proved every required level has full score or that no level is
// required. The course list is refreshed every two minutes per visible course;
// without this every refresh re-reads the progress of every finished assignment
// from Educoder through the shared egress address. Partial, failed or ambiguous
// reads are never stored, and a changed level count is a different key.
type settledProgressCache struct {
	mu      sync.Mutex
	now     func() time.Time
	ttl     time.Duration
	entries map[settledProgressKey]settledProgress
}

type settledProgressKey struct {
	owner      string
	course     string
	homework   int64
	challenges int
}

type settledProgress struct {
	required int
	at       time.Time
}

func newSettledProgressCache() *settledProgressCache {
	return &settledProgressCache{now: time.Now, ttl: settledProgressTTL, entries: map[settledProgressKey]settledProgress{}}
}

func (c *settledProgressCache) lookup(key settledProgressKey) (int, bool) {
	if c == nil || key.owner == "" {
		return 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return 0, false
	}
	if c.now().Sub(entry.at) >= c.ttl {
		delete(c.entries, key)
		return 0, false
	}
	return entry.required, true
}

// remember stores only settled states: all required levels full-scored, or no
// required level at all. Anything still to do is always read again.
func (c *settledProgressCache) remember(key settledProgressKey, required, completed int) {
	if c == nil || key.owner == "" || required < 0 || completed != required {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if len(c.entries) >= settledProgressMaxEntries {
		for k, entry := range c.entries {
			if now.Sub(entry.at) >= c.ttl {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= settledProgressMaxEntries {
			c.entries = map[settledProgressKey]settledProgress{}
		}
	}
	c.entries[key] = settledProgress{required: required, at: now}
}

func settledKey(owner, course string, homework educoder.Homework) settledProgressKey {
	return settledProgressKey{owner: owner, course: course, homework: homework.ID, challenges: homework.ChallengeCount}
}
