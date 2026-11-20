package web

import (
	"net/http"
	"net/http/httptest"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The vendored webfont package must be reachable exactly as the document links
// it, including the slice URLs the stylesheet resolves relative to itself. The
// real web directory is used so a missing or renamed vendored file fails here
// instead of in the browser.
func TestStaticServesVendoredWebfontPackage(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "web"))
	if err != nil {
		t.Fatal(err)
	}
	handler := newStaticHandler(root)
	get := func(target, encoding string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, target, nil)
		if encoding != "" {
			request.Header.Set("Accept-Encoding", encoding)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	for _, stylesheet := range []string{"/fonts/lxgw-wenkai/lxgwwenkai-regular.css", "/fonts/lxgw-wenkai/lxgwwenkai-bold.css"} {
		css := get(stylesheet, "gzip")
		if css.Code != http.StatusOK || css.Header().Get("Content-Type") != "text/css; charset=utf-8" {
			t.Fatalf("%s: status=%d type=%q", stylesheet, css.Code, css.Header().Get("Content-Type"))
		}
		if cache := css.Header().Get("Cache-Control"); !strings.Contains(cache, "immutable") {
			t.Fatalf("%s: font cache lifetime = %q", stylesheet, cache)
		}
		identity := get(stylesheet, "")
		slice := regexp.MustCompile(`url\('(\./files/[^']+\.woff2)'\)`).FindStringSubmatch(identity.Body.String())
		if len(slice) != 2 {
			t.Fatalf("%s publishes no woff2 slice URL", stylesheet)
		}
		// The browser resolves a relative slice URL against the stylesheet, so
		// the served path must be the one that resolution produces.
		target := path.Join(path.Dir(stylesheet), slice[1])
		woff2 := get(target, "gzip")
		if woff2.Code != http.StatusOK || woff2.Header().Get("Content-Type") != "font/woff2" || woff2.Body.Len() == 0 {
			t.Fatalf("%s: status=%d type=%q bytes=%d", target, woff2.Code, woff2.Header().Get("Content-Type"), woff2.Body.Len())
		}
		// Already-compressed payloads stay identity, so the body is the font.
		if woff2.Header().Get("Content-Encoding") != "" || !strings.HasPrefix(woff2.Body.String(), "wOF2") {
			t.Fatalf("%s: encoding=%q prefix=%q", target, woff2.Header().Get("Content-Encoding"), woff2.Body.String()[:min(4, woff2.Body.Len())])
		}
		if woff2.Header().Get("ETag") == "" {
			t.Fatalf("%s: missing validator", target)
		}
		conditional := httptest.NewRequest(http.MethodGet, target, nil)
		conditional.Header.Set("If-None-Match", woff2.Header().Get("ETag"))
		cached := httptest.NewRecorder()
		handler.ServeHTTP(cached, conditional)
		if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 {
			t.Fatalf("%s conditional GET: %d %d bytes", target, cached.Code, cached.Body.Len())
		}
	}
	// Neither the font walk nor the font cache policy may expose anything else
	// under web/, including sibling package files and directory paths.
	for _, target := range []string{
		"/fonts/", "/fonts/lxgw-wenkai/", "/fonts/lxgw-wenkai/files/",
		"/fonts/lxgw-wenkai/OFL.txt", "/fonts/lxgw-wenkai/style.css",
		"/fonts/lxgw-wenkai/../lxgw-wenkai/lxgwwenkai-regular.css",
		"/fonts/lxgw-wenkai/%2e%2e/lxgw-wenkai/lxgwwenkai-regular.css",
		"/fonts%5clxgw-wenkai/lxgwwenkai-regular.css",
		"/src/app.ts", "/check.mjs",
	} {
		response := get(target, "")
		if response.Code != http.StatusNotFound {
			t.Fatalf("private or invalid font path %s returned %d", target, response.Code)
		}
	}
}
