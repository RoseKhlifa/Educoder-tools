package compat

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type avatarRoundTripper func(*http.Request) (*http.Response, error)

func (f avatarRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func avatarResponse(status int, body []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}
}

func avatarJPEG(t *testing.T, red uint8) []byte {
	t.Helper()
	var buffer bytes.Buffer
	picture := image.NewRGBA(image.Rect(0, 0, 2, 2))
	picture.Set(0, 0, color.RGBA{R: red, G: 70, A: 255})
	if err := jpeg.Encode(&buffer, picture, nil); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func avatarServer(t *testing.T, roundTrip avatarRoundTripper) *Server {
	t.Helper()
	server := NewServer(Config{StaticDir: t.TempDir()})
	server.avatars.client.Transport = roundTrip
	return server
}

func avatarRequest(server *Server, method, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Cookie", "edureuse_session=private-session")
	request.Header.Set("Authorization", "private-token")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

func TestAvatarRoutesUseFixedSourcesAndValidatedBytes(t *testing.T) {
	picture := avatarJPEG(t, 180)
	var calls int
	server := avatarServer(t, func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Scheme != "https" || request.URL.Host != "data.educoder.net" || request.URL.Path != avatarPath+"student123" || request.URL.RawQuery != "" {
			t.Errorf("unexpected avatar destination: %s", request.URL.Redacted())
		}
		if request.Header.Get("Cookie") != "" || request.Header.Get("Authorization") != "" {
			t.Error("avatar request forwarded browser credentials")
		}
		if request.Header.Get("Referer") != "https://www.educoder.net/" {
			t.Error("missing official image Referer")
		}
		response := avatarResponse(200, picture)
		// Derive type from bytes even if the original server reports text/plain.
		response.Header.Set("Content-Type", "text/plain")
		response.Header.Set("Set-Cookie", "unexpected=private")
		return response, nil
	})
	for _, path := range []string{
		avatarPath + "student123?t=123",
		"/_assets/ali-cdn.educoder.net" + avatarPath + "student123",
		"/_assets/data.educoder.net" + avatarPath + "student123",
		"/_assets/public.educoder.net" + avatarPath + "student123",
	} {
		response := avatarRequest(server, http.MethodGet, path)
		if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), picture) {
			t.Fatalf("avatar path %q: status=%d", path, response.Code)
		}
		if response.Header().Get("Content-Type") != "image/jpeg" || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Set-Cookie") != "" {
			t.Fatalf("unexpected image response headers: %v", response.Header())
		}
	}
	response := avatarRequest(server, http.MethodHead, avatarPath+"student123")
	if response.Code != 200 || response.Body.Len() != 0 || response.Header().Get("Content-Length") == "0" {
		t.Fatalf("HEAD status=%d length=%s body=%d", response.Code, response.Header().Get("Content-Length"), response.Body.Len())
	}
	if calls != 1 {
		t.Fatalf("aliases and HEAD should reuse cache: calls=%d", calls)
	}
}

func TestAvatarCDNFallbackAndRealDefault(t *testing.T) {
	picture := avatarJPEG(t, 120)
	var seen []string
	server := avatarServer(t, func(request *http.Request) (*http.Response, error) {
		seen = append(seen, request.URL.Host+request.URL.Path)
		if request.URL.Path == avatarPath+"teacher" && request.URL.Host == "ali-cdn.educoder.net" || request.URL.Path == avatarPath+"b" {
			return avatarResponse(200, picture), nil
		}
		return avatarResponse(200, []byte("<!doctype html><html>CDN error</html>")), nil
	})
	response := avatarRequest(server, http.MethodGet, avatarPath+"teacher")
	if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), picture) {
		t.Fatalf("CDN fallback did not produce the real image: status=%d", response.Code)
	}
	if len(seen) != 2 || !strings.HasPrefix(seen[0], "data.") || !strings.HasPrefix(seen[1], "ali-cdn.") {
		t.Fatalf("unexpected source fallback: %v", seen)
	}
	seen = nil
	response = avatarRequest(server, http.MethodGet, avatarPath+"missing")
	if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), picture) {
		t.Fatalf("missing avatar did not use official default: status=%d", response.Code)
	}
	if len(seen) != 3 || seen[2] != "data.educoder.net"+avatarPath+"b" {
		t.Fatalf("expected real default after both original sources failed: %v", seen)
	}
}

func TestAvatarRejectsDestinationOverridesAndTraversal(t *testing.T) {
	calls := 0
	server := avatarServer(t, func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected upstream call")
	})
	for _, path := range []string{
		avatarPath, avatarPath + "../private", avatarPath + "%2e%2e%2fprivate", avatarPath + "a%5cb",
		avatarPath + "a/b", avatarPath + strings.Repeat("a", 129), avatarPath + "a?url=https://example.com",
		avatarPath + "a?host=example.com", avatarPath + "a?t=1&t=2", avatarPath + "a?t=unexpected",
		"/_assets/ali-cdn.educoder.net" + avatarPath + "a?upstream=localhost",
		"/_assets/unrelated.example" + avatarPath + "a",
	} {
		response := avatarRequest(server, http.MethodGet, path)
		if response.Code != http.StatusNotFound {
			t.Errorf("path=%q status=%d, want 404", path, response.Code)
		}
	}
	response := avatarRequest(server, http.MethodPost, avatarPath+"a")
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("POST status=%d allow=%s", response.Code, response.Header().Get("Allow"))
	}
	if calls != 0 {
		t.Errorf("malformed avatar path triggered %d upstream requests", calls)
	}
}

func TestAvatarRejectsRedirectHTMLAndOversizedResponses(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
		code int
	}{
		{"redirect", nil, 302},
		{"html", []byte("<html>error</html>"), 200},
		{"oversized", bytes.Repeat([]byte{0}, avatarMaxBytes+1), 200},
		{"invalid-jpeg", []byte{0xff, 0xd8, 0xff, 0xe0}, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := avatarServer(t, func(request *http.Request) (*http.Response, error) {
				calls++
				if request.URL.Host != "data.educoder.net" && request.URL.Host != "ali-cdn.educoder.net" {
					t.Fatal("followed untrusted redirect")
				}
				response := avatarResponse(tc.code, tc.body)
				response.Header.Set("Location", "https://unrelated.example/private")
				response.Header.Set("Content-Type", "image/jpeg")
				return response, nil
			})
			response := avatarRequest(server, http.MethodGet, avatarPath+"b")
			if response.Code != http.StatusBadGateway || strings.HasPrefix(response.Header().Get("Content-Type"), "image/") || calls != 2 {
				t.Fatalf("status=%d type=%s calls=%d", response.Code, response.Header().Get("Content-Type"), calls)
			}
		})
	}
	if _, err := readAvatar(io.LimitReader(bytes.NewReader(bytes.Repeat([]byte{0}, avatarMaxBytes+1)), avatarMaxBytes+1)); err == nil {
		t.Fatal("streaming body exceeded byte limit")
	}
}

func TestAvatarPreservesValidLocalImages(t *testing.T) {
	picture := avatarJPEG(t, 40)
	server := avatarServer(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("local avatar unexpectedly requested the network")
		return nil, errAvatarUnavailable
	})
	path := filepath.Join(server.config.StaticDir, "public.educoder.net", filepath.FromSlash(avatarPath), "local-user")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, picture, 0o600); err != nil {
		t.Fatal(err)
	}
	response := avatarRequest(server, http.MethodGet, avatarPath+"local-user")
	if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), picture) {
		t.Fatalf("local status=%d", response.Code)
	}
}

func TestAvatarCacheTTLAndBounds(t *testing.T) {
	picture := avatarJPEG(t, 40)
	var calls int
	server := avatarServer(t, func(*http.Request) (*http.Response, error) {
		calls++
		return avatarResponse(200, picture), nil
	})
	service := server.avatars
	now := time.Now()
	service.now = func() time.Time { return now }
	service.maxCount = 2
	service.maxBytes = 2 * len(picture)
	for _, key := range []string{"one", "two", "one"} {
		if _, err := service.get(context.Background(), key); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Second)
	}
	if calls != 2 {
		t.Fatalf("cache missed repeated key: calls=%d", calls)
	}
	if _, err := service.get(context.Background(), "three"); err != nil {
		t.Fatal(err)
	}
	if len(service.cache) != 2 || service.bytes > service.maxBytes {
		t.Fatalf("cache unbounded: count=%d bytes=%d", len(service.cache), service.bytes)
	}
	if _, found := service.cache["two"]; found {
		t.Fatal("cache did not evict least recently used entry")
	}
	now = now.Add(avatarTTL)
	if _, err := service.get(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if calls != 4 || len(service.cache) != 1 {
		t.Fatalf("TTL did not expire old entries: calls=%d cache=%d", calls, len(service.cache))
	}
}

func TestAvatarConcurrentRequestsShareSingleFetch(t *testing.T) {
	picture := avatarJPEG(t, 100)
	var calls atomic.Int32
	entered := make(chan struct{})
	resume := make(chan struct{})
	server := avatarServer(t, func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-resume:
			return avatarResponse(200, picture), nil
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
	})
	var wait sync.WaitGroup
	for range 12 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := server.avatars.get(context.Background(), "same-user")
			if err != nil || !bytes.Equal(result.body, picture) {
				t.Error("concurrent avatar fetch failed")
			}
		}()
	}
	<-entered
	close(resume)
	wait.Wait()
	if calls.Load() != 1 {
		t.Fatalf("concurrent requests fetched %d times", calls.Load())
	}
}

func TestAvatarRequestCancellationStopsFurtherSources(t *testing.T) {
	entered := make(chan struct{})
	var calls atomic.Int32
	server := avatarServer(t, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		close(entered)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	if server.avatars.client.Timeout > 10*time.Second {
		t.Fatal("individual avatar fetch timeout exceeds 10 seconds")
	}
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, avatarPath+"student", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		server.ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()
	<-entered
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("avatar handler ignored canceled request")
	}
	if calls.Load() != 1 {
		t.Errorf("request cancellation still tried more sources: calls=%d", calls.Load())
	}
}
