package compat

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"

	"educoder-cli/internal/educoder"
)

type session struct {
	client  *educoder.Client
	login   string
	marker  string
	expires time.Time
}

type sessionStore struct {
	mu    sync.RWMutex
	items map[string]session
	ttl   time.Duration
	now   func() time.Time
}

func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{items: make(map[string]session), ttl: ttl, now: time.Now}
}

func randomID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *sessionStore) create(client *educoder.Client, login string) (string, session, error) {
	id, err := randomID()
	if err != nil {
		return "", session{}, err
	}
	marker, err := randomID()
	if err != nil {
		return "", session{}, err
	}
	item := session{client: client, login: login, marker: "local-" + marker, expires: s.now().Add(s.ttl)}
	s.mu.Lock()
	s.items[id] = item
	s.mu.Unlock()
	return id, item, nil
}

func (s *sessionStore) get(id string) (session, bool) {
	s.mu.RLock()
	item, ok := s.items[id]
	s.mu.RUnlock()
	if ok && !item.expires.After(s.now()) {
		s.remove(id)
		return session{}, false
	}
	return item, ok
}

func (s *sessionStore) remove(id string) {
	s.mu.Lock()
	delete(s.items, id)
	s.mu.Unlock()
}

func (s *sessionStore) sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for key, item := range s.items {
		if !item.expires.After(now) {
			delete(s.items, key)
		}
	}
}
