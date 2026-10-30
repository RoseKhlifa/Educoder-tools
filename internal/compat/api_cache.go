package compat

import (
	"container/list"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// apiResponse bodies are sanitized and localized before entering the cache.
// After construction their bytes are immutable; handlers only write them.
type apiResponse struct {
	status     int
	body       []byte
	upstream   time.Duration
	processing time.Duration
}

type apiCacheKey struct {
	scope, method, path, query string
}

type apiCacheEntry struct {
	key     apiCacheKey
	value   apiResponse
	expires time.Time
}

type apiFlight struct {
	done  chan struct{}
	value apiResponse
	err   error
}

type apiResponseCache struct {
	mu         sync.Mutex
	items      map[apiCacheKey]*list.Element
	flights    map[apiCacheKey]*apiFlight
	lru        *list.List
	bytes      int
	maxBytes   int
	maxEntries int
	now        func() time.Time
}

func newAPIResponseCache() *apiResponseCache {
	return &apiResponseCache{
		items: make(map[apiCacheKey]*list.Element), flights: make(map[apiCacheKey]*apiFlight),
		lru: list.New(), maxBytes: 32 << 20, maxEntries: 512, now: time.Now,
	}
}

func apiCacheTTL(method, path string) time.Duration {
	if method != http.MethodGet {
		return 0
	}
	switch path {
	case "/api/setting.json":
		return 5 * time.Minute
	case "/api/users/system_update.json":
		return 30 * time.Second
	case "/api/home/index.json":
		return time.Minute
	case "/api/users/get_user_info.json":
		return 15 * time.Second
	case "/api/users/get_notice.json":
		return 5 * time.Second
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 4 || parts[0] != "api" {
		return 0
	}
	switch parts[1] {
	case "users":
		if parts[3] == "courses.json" || parts[3] == "homepage_info.json" {
			return 15 * time.Second
		}
	case "courses":
		switch parts[3] {
		case "top_banner.json", "left_banner.json":
			return time.Minute
		case "homework_commons.json", "informs.json":
			return 10 * time.Second
		}
	}
	// GET does not imply read-only: shixun_exec, version_repository_delete and
	// popup counters can change state. Task/code content must always be fresh.
	return 0
}

func apiCacheBypass(r *http.Request) bool {
	for _, directive := range strings.Split(r.Header.Get("Cache-Control"), ",") {
		name := strings.ToLower(strings.TrimSpace(strings.SplitN(directive, "=", 2)[0]))
		if name == "no-cache" || name == "no-store" {
			return true
		}
	}
	return false
}

func makeAPICacheKey(scope, method, path string, query url.Values) apiCacheKey {
	effective := make(url.Values, len(query))
	for key, values := range query {
		effective[key] = append([]string(nil), values...)
	}
	// RecordedRequest discards this browser identity hint and binds the login
	// from the server-side client. Scope already binds the authenticated user.
	effective.Del("zzud")
	return apiCacheKey{scope: scope, method: method, path: path, query: effective.Encode()}
}

func cacheableAPIResponse(value apiResponse) bool {
	if value.status != http.StatusOK || len(value.body) == 0 {
		return false
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(value.body, &root) != nil {
		return false
	}
	if status, exists := root["status"]; exists {
		var number float64
		if json.Unmarshal(status, &number) == nil && number != 0 {
			return false
		}
	}
	// Several upstream routes report errors in JSON while returning HTTP 200.
	for _, field := range []string{"error", "errors"} {
		if raw, ok := root[field]; ok && string(raw) != "null" && string(raw) != "false" && string(raw) != `""` && string(raw) != "[]" && string(raw) != "{}" {
			return false
		}
	}
	return true
}

// get coalesces identical outstanding reads without serializing unrelated API
// calls. A cancelled browser navigation stops waiting immediately; another
// navigation can reuse the bounded shared fetch instead of restarting it.
func (c *apiResponseCache) get(ctx context.Context, key apiCacheKey, ttl time.Duration, fetch func(context.Context) (apiResponse, error)) (apiResponse, string, error) {
	if err := ctx.Err(); err != nil {
		return apiResponse{}, "BYPASS", err
	}
	c.mu.Lock()
	if element, ok := c.items[key]; ok {
		entry := element.Value.(*apiCacheEntry)
		if entry.expires.After(c.now()) {
			c.lru.MoveToFront(element)
			value := entry.value
			value.upstream, value.processing = 0, 0
			c.mu.Unlock()
			return value, "HIT", nil
		}
		c.remove(element)
	}
	flight, exists := c.flights[key]
	state := "COALESCED"
	if !exists {
		flight = &apiFlight{done: make(chan struct{})}
		c.flights[key] = flight
		state = "MISS"
	}
	c.mu.Unlock()
	if !exists {
		go func() {
			fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
			defer cancel()
			value, err := fetch(fetchCtx)
			c.mu.Lock()
			if err == nil && cacheableAPIResponse(value) && len(value.body) <= c.maxBytes && c.maxEntries > 0 {
				// Own these bytes even if a fetch implementation retains its buffer.
				value.body = append([]byte(nil), value.body...)
				entry := &apiCacheEntry{key: key, value: value, expires: c.now().Add(ttl)}
				c.items[key] = c.lru.PushFront(entry)
				c.bytes += len(value.body)
				for c.bytes > c.maxBytes || c.lru.Len() > c.maxEntries {
					c.remove(c.lru.Back())
				}
			}
			flight.value, flight.err = value, err
			delete(c.flights, key)
			close(flight.done)
			c.mu.Unlock()
		}()
	}
	select {
	case <-ctx.Done():
		return apiResponse{}, state, ctx.Err()
	case <-flight.done:
		return flight.value, state, flight.err
	}
}

func (c *apiResponseCache) remove(element *list.Element) {
	entry := element.Value.(*apiCacheEntry)
	delete(c.items, entry.key)
	c.bytes -= len(entry.value.body)
	c.lru.Remove(element)
}

func (c *apiResponseCache) sweep() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for _, element := range c.items {
		if !element.Value.(*apiCacheEntry).expires.After(now) {
			c.remove(element)
		}
	}
}
