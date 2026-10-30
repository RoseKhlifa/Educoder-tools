package compat

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func staticTestFile(t *testing.T, name, content string) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "www.educoder.net", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return NewServer(Config{StaticDir: root}), path
}

func staticRequest(server *Server, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	server.ServeHTTP(w, r)
	return w
}

func TestStaticCompressedRepresentationAndConditionalRequests(t *testing.T) {
	original := strings.Repeat("window.example = 'recorded build';\n", 8192)
	server, _ := staticTestFile(t, "app.abcdef12.js", original)
	url := "/_assets/www.educoder.net/app.abcdef12.js"
	w := staticRequest(server, http.MethodGet, url, map[string]string{"Accept-Encoding": "br, gzip"})
	if w.Code != http.StatusOK || w.Header().Get("Content-Encoding") != "gzip" || w.Body.Len() >= len(original)/3 {
		t.Fatalf("gzip status=%d encoding=%q bytes=%d original=%d", w.Code, w.Header().Get("Content-Encoding"), w.Body.Len(), len(original))
	}
	reader, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	decompressed, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || string(decompressed) != original {
		t.Fatal("compressed representation changed published resource bytes", err)
	}
	if w.Header().Get("Content-Length") != strconv.Itoa(w.Body.Len()) || w.Header().Get("Vary") != "Accept-Encoding" || !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("incorrect representation headers: %v", w.Header())
	}
	if w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatal("fingerprinted resource was not immutable")
	}
	compressedETag := w.Header().Get("ETag")
	identity := staticRequest(server, http.MethodGet, url, nil)
	if identity.Code != http.StatusOK || identity.Body.String() != original || identity.Header().Get("ETag") == compressedETag {
		t.Fatal("identity representation or representation-specific ETag is incorrect")
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		conditional := staticRequest(server, method, url, map[string]string{"Accept-Encoding": "gzip", "If-None-Match": compressedETag})
		if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 || conditional.Header().Get("ETag") != compressedETag {
			t.Fatalf("conditional %s: status=%d bytes=%d etag=%q", method, conditional.Code, conditional.Body.Len(), conditional.Header().Get("ETag"))
		}
	}
	head := staticRequest(server, http.MethodHead, url, map[string]string{"Accept-Encoding": "gzip"})
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != w.Header().Get("Content-Length") || head.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("HEAD differs from GET metadata: %v", head.Header())
	}
	identityConditional := staticRequest(server, http.MethodGet, url, map[string]string{"If-None-Match": identity.Header().Get("ETag")})
	if identityConditional.Code != http.StatusNotModified {
		t.Fatalf("identity conditional status=%d", identityConditional.Code)
	}
	modifiedConditional := staticRequest(server, http.MethodGet, url, map[string]string{"If-Modified-Since": identity.Header().Get("Last-Modified")})
	if modifiedConditional.Code != http.StatusNotModified {
		t.Fatalf("modification-date conditional status=%d", modifiedConditional.Code)
	}
	partial := staticRequest(server, http.MethodGet, url, map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=7-20", "If-Range": identity.Header().Get("ETag")})
	if partial.Code != http.StatusPartialContent || partial.Body.String() != original[7:21] || partial.Header().Get("Content-Encoding") != "" {
		t.Fatalf("identity range status=%d body=%q headers=%v", partial.Code, partial.Body.String(), partial.Header())
	}
	invalidRange := staticRequest(server, http.MethodGet, url, map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=9999999-"})
	if invalidRange.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("out-of-bounds range status=%d", invalidRange.Code)
	}
}

func TestStaticEncodingNegotiation(t *testing.T) {
	server, _ := staticTestFile(t, "styles.css", strings.Repeat("body { color: #123; }\n", 100))
	for _, tc := range []struct {
		encoding string
		gzip     bool
	}{
		{"", false}, {"br", false}, {"gzip", true}, {"GZip; q=0.5", true},
		{"gzip;q=0", false}, {"gzip;q=0,*;q=1", false}, {"gzip;q=0.1,*;q=0", true},
		{"*;q=1", true}, {"*;q=0", false}, {"gzip;q=invalid", false},
		{"gzip;q=2", false}, {"gzip;q=-1", false},
	} {
		t.Run(tc.encoding, func(t *testing.T) {
			w := staticRequest(server, http.MethodGet, "/styles.css", map[string]string{"Accept-Encoding": tc.encoding})
			if w.Code != http.StatusOK || (w.Header().Get("Content-Encoding") == "gzip") != tc.gzip {
				t.Fatalf("encoding=%q status=%d compressed=%q", tc.encoding, w.Code, w.Header().Get("Content-Encoding"))
			}
		})
	}
}

func TestStaticHTMLRevalidatesAndCacheTracksChanges(t *testing.T) {
	original := "<html>" + strings.Repeat("original build ", 100) + "</html>"
	server, path := staticTestFile(t, "index.html", original)
	first := staticRequest(server, http.MethodGet, "/users/student/classrooms", nil)
	if first.Code != http.StatusOK || first.Body.String() != original || first.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("HTML did not require revalidation: %v", first.Header())
	}
	replacement := strings.ReplaceAll(original, "original", "modified")
	if err := os.WriteFile(path, []byte(replacement), 0o600); err != nil {
		t.Fatal(err)
	}
	// Force a distinct timestamp even on filesystems with coarse resolution.
	when := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	second := staticRequest(server, http.MethodGet, "/users/student/classrooms", map[string]string{"If-None-Match": first.Header().Get("ETag")})
	if second.Code != http.StatusOK || second.Body.String() != replacement || first.Header().Get("ETag") == second.Header().Get("ETag") {
		t.Fatal("changed file served old cache content or validator")
	}
}

func TestStaticCacheBoundedConcurrentRequests(t *testing.T) {
	cache := newStaticAssetCache(2048)
	root := t.TempDir()
	paths := make([]string, 20)
	for index := range paths {
		paths[index] = filepath.Join(root, strconv.Itoa(index)+".js")
		if err := os.WriteFile(paths[index], []byte(strings.Repeat("window.value = '"+strconv.Itoa(index)+"';\n", 100)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var workers sync.WaitGroup
	for index := 0; index < 64; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			path := paths[index%len(paths)]
			info, err := os.Stat(path)
			if err != nil {
				t.Error(err)
				return
			}
			asset, err := cache.get(path, info)
			if err != nil || asset == nil || len(asset.compressed) == 0 {
				t.Errorf("concurrent asset missing: %v", err)
			}
		}(index)
	}
	workers.Wait()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	var actual int64
	for _, element := range cache.entries {
		actual += element.Value.(*staticAsset).cost
	}
	if cache.used > cache.limit || cache.used != actual || len(cache.entries) != cache.order.Len() || len(cache.inflight) != 0 || len(cache.entries) >= len(paths) {
		t.Fatalf("cache bound/accounting: used=%d actual=%d limit=%d entries=%d order=%d flights=%d", cache.used, actual, cache.limit, len(cache.entries), cache.order.Len(), len(cache.inflight))
	}
}

func TestStaticBinaryAndLargeFileStayIdentity(t *testing.T) {
	server, _ := staticTestFile(t, "font.abcdef12.woff2", strings.Repeat("compressed font bytes", 100))
	w := staticRequest(server, http.MethodGet, "/font.abcdef12.woff2", map[string]string{"Accept-Encoding": "gzip"})
	if w.Code != http.StatusOK || w.Header().Get("Content-Encoding") != "" || w.Header().Get("Vary") != "" {
		t.Fatalf("binary resource was compressed: %v", w.Header())
	}
	largePath := filepath.Join(server.config.StaticDir, "www.educoder.net", "large.js")
	file, err := os.Create(largePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxCompressedStaticSize + 1); err != nil {
		t.Fatal(err)
	}
	file.Close()
	large := staticRequest(server, http.MethodHead, "/large.js", map[string]string{"Accept-Encoding": "gzip"})
	if large.Code != http.StatusOK || large.Header().Get("Content-Encoding") != "" || !strings.HasPrefix(large.Header().Get("ETag"), `W/"`) || large.Body.Len() != 0 {
		t.Fatalf("large asset buffered/compressed: %v", large.Header())
	}
}
