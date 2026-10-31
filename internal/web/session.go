package web

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"educoder-cli/internal/educoder"
	"educoder-cli/internal/platform"
)

// Session is the server-side state associated with one browser session. The
// upstream Educoder client (and therefore its cookies) never leaves this
// structure or reaches the browser.
type Session struct {
	Client    EducoderClient
	User      educoder.User
	ExpiresAt time.Time
	AvatarKey string
	Principal platform.User
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
	return s.createPrincipal(client, user, avatarKey, platform.User{})
}

func (s *SessionStore) createPrincipal(client EducoderClient, user educoder.User, avatarKey string, principal platform.User) (string, error) {
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
	s.sessions[id] = Session{Client: client, User: user, ExpiresAt: s.now().Add(s.ttl), AvatarKey: avatarKey, Principal: principal}
	s.mu.Unlock()
	return id, nil
}

// DeleteUser revokes all browser sessions for a disabled platform user.
func (s *SessionStore) DeleteUser(userID string) {
	if userID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, session := range s.sessions {
		if session.Principal.ID == userID {
			delete(s.sessions, id)
		}
	}
}

func (s *SessionStore) UserIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	ids := []string{}
	for _, session := range s.sessions {
		if id := session.Principal.ID; id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
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
