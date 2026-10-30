package compat

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	avatarPath       = "/images/avatars/User/"
	avatarMaxBytes   = 2 << 20
	avatarCacheBytes = 32 << 20
	avatarCacheCount = 256
	avatarTTL        = 20 * time.Minute
)

var errAvatarUnavailable = errors.New("public avatar unavailable")

type avatarImage struct {
	body    []byte
	kind    string
	expires time.Time
	used    time.Time
}

type avatarFlight struct {
	done  chan struct{}
	image avatarImage
	err   error
}

// avatarService reads just the public User avatar resource family from two
// fixed official sources. It has no upstream session, accepts no destination
// URL from the browser and never persists user images to disk.
type avatarService struct {
	staticDir string
	client    *http.Client
	mu        sync.Mutex
	cache     map[string]avatarImage
	flights   map[string]*avatarFlight
	bytes     int
	maxBytes  int
	maxCount  int
	ttl       time.Duration
	now       func() time.Time
	requests  chan struct{}
}

func newAvatarService(staticDir string) *avatarService {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 6 * time.Second
	transport.MaxResponseHeaderBytes = 32 << 10
	return &avatarService{
		staticDir: staticDir,
		client: &http.Client{
			Transport: transport, Timeout: 6 * time.Second,
			// Official avatar URLs do not need redirects. In particular, never
			// follow a redirect to another host or a different resource family.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		cache: map[string]avatarImage{}, flights: map[string]*avatarFlight{},
		maxBytes: avatarCacheBytes, maxCount: avatarCacheCount,
		ttl: avatarTTL, now: time.Now, requests: make(chan struct{}, 8),
	}
}

// matchAvatarPath also recognizes malformed paths in this resource family so
// they cannot fall through to the static file or SPA handler.
func matchAvatarPath(path string) (string, bool) {
	if strings.HasPrefix(path, avatarPath) {
		return strings.TrimPrefix(path, avatarPath), true
	}
	for _, host := range []string{"ali-cdn.educoder.net", "data.educoder.net", "public.educoder.net"} {
		prefix := "/_assets/" + host + avatarPath
		if strings.HasPrefix(path, prefix) {
			return strings.TrimPrefix(path, prefix), true
		}
	}
	return "", false
}

func validAvatarKey(key string) bool {
	if len(key) == 0 || len(key) > 128 {
		return false
	}
	for _, char := range key {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_') {
			return false
		}
	}
	return true
}

func validAvatarQuery(raw string) bool {
	query, err := url.ParseQuery(raw)
	if err != nil {
		return false
	}
	for key, values := range query {
		if key != "t" || len(values) != 1 || len(values[0]) > 32 {
			return false
		}
		for _, char := range values[0] {
			if char < '0' || char > '9' {
				return false
			}
		}
	}
	return true
}

func (s *Server) serveAvatar(w http.ResponseWriter, r *http.Request) bool {
	key, matched := matchAvatarPath(r.URL.Path)
	if !matched {
		return false
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !validAvatarKey(key) || !validAvatarQuery(r.URL.RawQuery) {
		http.NotFound(w, r)
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	result, err := s.avatars.get(ctx, key)
	if err != nil && key != "b" && ctx.Err() == nil {
		// This is Educoder's actual public default avatar, not a generated
		// placeholder. Cache it separately from the requested user's image.
		result, err = s.avatars.get(ctx, "b")
	}
	if err != nil {
		http.Error(w, "avatar temporarily unavailable", http.StatusBadGateway)
		return true
	}
	w.Header().Set("Content-Type", result.kind)
	w.Header().Set("Content-Length", strconv.Itoa(len(result.body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(result.body)
	}
	return true
}

func (a *avatarService) get(ctx context.Context, key string) (avatarImage, error) {
	a.mu.Lock()
	now := a.now()
	if result, ok := a.cache[key]; ok {
		if now.Before(result.expires) {
			result.used = now
			a.cache[key] = result
			a.mu.Unlock()
			return result, nil
		}
		a.bytes -= len(result.body)
		delete(a.cache, key)
	}
	if flight, ok := a.flights[key]; ok {
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return avatarImage{}, ctx.Err()
		case <-flight.done:
			return flight.image, flight.err
		}
	}
	flight := &avatarFlight{done: make(chan struct{})}
	a.flights[key] = flight
	a.mu.Unlock()

	result, err := a.load(ctx, key)
	a.mu.Lock()
	if err == nil {
		result.expires, result.used = a.now().Add(a.ttl), a.now()
		a.putLocked(key, result)
	}
	flight.image, flight.err = result, err
	delete(a.flights, key)
	close(flight.done)
	a.mu.Unlock()
	return result, err
}

func (a *avatarService) putLocked(key string, result avatarImage) {
	if len(result.body) > a.maxBytes || a.maxCount <= 0 {
		return
	}
	now := a.now()
	for name, cached := range a.cache {
		if !now.Before(cached.expires) {
			a.bytes -= len(cached.body)
			delete(a.cache, name)
		}
	}
	for len(a.cache) >= a.maxCount || a.bytes+len(result.body) > a.maxBytes {
		oldest := ""
		var used time.Time
		for name, cached := range a.cache {
			if oldest == "" || cached.used.Before(used) {
				oldest, used = name, cached.used
			}
		}
		a.bytes -= len(a.cache[oldest].body)
		delete(a.cache, oldest)
	}
	a.cache[key] = result
	a.bytes += len(result.body)
}

func (a *avatarService) load(ctx context.Context, key string) (avatarImage, error) {
	// Previously downloaded images remain useful, including during an upstream
	// outage. Only valid, bounded image bytes inside the static tree qualify.
	for _, host := range []string{"data.educoder.net", "ali-cdn.educoder.net", "public.educoder.net"} {
		file, ok := localFile(filepath.Join(a.staticDir, host), strings.TrimPrefix(avatarPath, "/")+key)
		if !ok {
			continue
		}
		reader, err := os.Open(file)
		if err != nil {
			continue
		}
		result, err := readAvatar(reader)
		_ = reader.Close()
		if err == nil {
			return result, nil
		}
	}
	for _, host := range []string{"data.educoder.net", "ali-cdn.educoder.net"} {
		if ctx.Err() != nil {
			return avatarImage{}, ctx.Err()
		}
		result, err := a.fetch(ctx, host, key)
		if err == nil {
			return result, nil
		}
	}
	return avatarImage{}, errAvatarUnavailable
}

func (a *avatarService) fetch(ctx context.Context, host, key string) (avatarImage, error) {
	select {
	case a.requests <- struct{}{}:
		defer func() { <-a.requests }()
	case <-ctx.Done():
		return avatarImage{}, ctx.Err()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+avatarPath+key, nil)
	if err != nil {
		return avatarImage{}, errAvatarUnavailable
	}
	request.Header.Set("Referer", "https://www.educoder.net/")
	request.Header.Set("Accept", "image/avif,image/webp,image/png,image/jpeg,image/gif")
	request.Header.Set("User-Agent", "Educoder-tools/avatars")
	response, err := a.client.Do(request)
	if err != nil {
		return avatarImage{}, errAvatarUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > avatarMaxBytes {
		return avatarImage{}, errAvatarUnavailable
	}
	return readAvatar(response.Body)
}

func readAvatar(reader io.Reader) (avatarImage, error) {
	body, err := io.ReadAll(io.LimitReader(reader, avatarMaxBytes+1))
	if err != nil || len(body) == 0 || len(body) > avatarMaxBytes {
		return avatarImage{}, errAvatarUnavailable
	}
	kind := http.DetectContentType(body)
	switch kind {
	case "image/jpeg", "image/png", "image/gif":
		config, _, err := image.DecodeConfig(bytes.NewReader(body))
		if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > 16384 || config.Height > 16384 {
			return avatarImage{}, errAvatarUnavailable
		}
	case "image/webp":
		if len(body) < 20 || string(body[:4]) != "RIFF" || string(body[8:12]) != "WEBP" || int(binary.LittleEndian.Uint32(body[4:8]))+8 != len(body) {
			return avatarImage{}, errAvatarUnavailable
		}
	default:
		// AVIF stores its format in the ISO BMFF ftyp box. Unlike SVG, it
		// contains no executable markup and is a valid original avatar format.
		if len(body) < 24 || string(body[4:8]) != "ftyp" {
			return avatarImage{}, errAvatarUnavailable
		}
		boxSize := int(binary.BigEndian.Uint32(body[:4]))
		if boxSize < 24 || boxSize > len(body) || boxSize > 256 {
			return avatarImage{}, errAvatarUnavailable
		}
		avif := string(body[8:12]) == "avif" || string(body[8:12]) == "avis"
		for offset := 16; offset+4 <= boxSize; offset += 4 {
			avif = avif || string(body[offset:offset+4]) == "avif" || string(body[offset:offset+4]) == "avis"
		}
		if !avif {
			return avatarImage{}, errAvatarUnavailable
		}
		kind = "image/avif"
	}
	return avatarImage{body: body, kind: kind}, nil
}
