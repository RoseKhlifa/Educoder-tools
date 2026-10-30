package web

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"educoder-cli/internal/educoder"
)

// Session is the server-side state associated with one browser session. The
// upstream Educoder client (and therefore its cookies) never leaves this
// structure or reaches the browser.
type Session struct {
	Client    EducoderClient
	User      educoder.User
	ExpiresAt time.Time
	AvatarKey string
}

// SessionStore keeps sessions in memory. A mutex protects both reads and
// writes because the HTTP server handles requests concurrently.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]Session
	ttl      time.Duration
	now      func() time.Time
}

func NewSessionStore(ttl time.Duration) *SessionStore {
	if ttl <= 0 {
		ttl = 8 * time.Hour
	}
	return &SessionStore{sessions: make(map[string]Session), ttl: ttl, now: time.Now}
}

func (s *SessionStore) Create(client EducoderClient, user educoder.User) (string, error) {
	return s.createWithAvatar(client, user, "b")
}

func (s *SessionStore) createWithAvatar(client EducoderClient, user educoder.User, avatarKey string) (string, error) {
	if client == nil {
		return "", errors.New("nil client")
	}
	// A 256-bit random opaque value is sent only as an HttpOnly cookie.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	s.sessions[id] = Session{Client: client, User: user, ExpiresAt: s.now().Add(s.ttl), AvatarKey: avatarKey}
	s.mu.Unlock()
	return id, nil
}

func (s *SessionStore) Get(id string) (Session, bool) {
	if id == "" {
		return Session{}, false
	}
	now := s.now()
	s.mu.RLock()
	session, ok := s.sessions[id]
	s.mu.RUnlock()
	if !ok {
		return Session{}, false
	}
	if !session.ExpiresAt.After(now) {
		s.Delete(id)
		return Session{}, false
	}
	return session, true
}

func (s *SessionStore) Delete(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// Sweep removes expired entries. It is intentionally explicit so callers can
// run it from a lightweight ticker without a goroutine per session.
func (s *SessionStore) Sweep() {
	now := s.now()
	s.mu.Lock()
	for id, session := range s.sessions {
		if !session.ExpiresAt.After(now) {
			delete(s.sessions, id)
		}
	}
	s.mu.Unlock()
}

func (s *SessionStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}
