package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEntryAndAPISignalSameRevisionAndChangeWhenEntryChanges(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "dist"), 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"index.html": `<html><head></head><body><script src="./dist/app.js?v=0.1.55"></script></body></html>`, "dist/app.js": "export const entry=1;"}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := DefaultConfig()
	cfg.StaticDir = dir
	s := NewServer(cfg)
	page := httptest.NewRecorder()
	s.ServeHTTP(page, httptest.NewRequest("GET", "/", nil))
	revision := page.Header().Get("X-App-Asset-Revision")
	if len(revision) != 16 || !strings.Contains(page.Body.String(), `content="`+revision+`"`) || !strings.Contains(page.Body.String(), `__asset=`+revision) {
		t.Fatal("document failed to bind entry revision")
	}
	api := httptest.NewRecorder()
	s.ServeHTTP(api, httptest.NewRequest("GET", "/api/auth/me", nil))
	if api.Code != http.StatusUnauthorized || api.Header().Get("X-App-Asset-Revision") != revision {
		t.Fatal("API version signal missing")
	}
	if err := os.WriteFile(filepath.Join(dir, "dist/app.js"), []byte("export const entry=2;"), 0600); err != nil {
		t.Fatal(err)
	}
	updated := httptest.NewRecorder()
	NewServer(cfg).ServeHTTP(updated, httptest.NewRequest("GET", "/", nil))
	if updated.Header().Get("X-App-Asset-Revision") == revision || updated.Header().Get("ETag") == page.Header().Get("ETag") {
		t.Fatal("entry cache failed to change with application")
	}
}
