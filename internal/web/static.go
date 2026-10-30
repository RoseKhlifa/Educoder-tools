package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type staticAsset struct {
	body, gzip []byte
	kind, etag string
	modified   time.Time
}

type staticHandler map[string]staticAsset

// Cache only public build outputs at startup. Source files, build helpers,
// symlinks, directory listings and arbitrary SPA paths are never exposed.
func newStaticHandler(dir string) http.Handler {
	assets := staticHandler{}
	load := func(name, kind string) {
		filename := filepath.Join(dir, filepath.FromSlash(name))
		info, err := os.Lstat(filename)
		if err != nil || !info.Mode().IsRegular() {
			return
		}
		body, err := os.ReadFile(filename)
		if err != nil {
			return
		}
		var compressed bytes.Buffer
		zw := gzip.NewWriter(&compressed)
		_, _ = zw.Write(body)
		_ = zw.Close()
		assets["/"+name] = staticAsset{body: body, gzip: compressed.Bytes(), kind: kind, etag: fmt.Sprintf("%x", sha256.Sum256(body)), modified: info.ModTime()}
	}
	load("index.html", "text/html; charset=utf-8")
	load("styles.css", "text/css; charset=utf-8")
	dist := filepath.Join(dir, "dist")
	// WalkDir never follows symlinks, including a dist symlink. This prevents
	// accidentally packaging or serving files outside the public build tree.
	_ = filepath.WalkDir(dist, func(filename string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		relative, err := filepath.Rel(dir, filename)
		if err != nil {
			return nil
		}
		name := filepath.ToSlash(relative)
		if publicJS(name) {
			load(name, "text/javascript; charset=utf-8")
		}
		return nil
	})
	return assets
}

func publicJS(name string) bool {
	if !strings.HasPrefix(name, "dist/") || !strings.HasSuffix(name, ".js") || strings.ContainsAny(name, `\:`) {
		return false
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || strings.HasPrefix(segment, ".") {
			return false
		}
	}
	return true
}

func (s staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	name := r.URL.Path
	if strings.ContainsAny(name, `\:`) || path.Clean(name) != name || strings.ContainsRune(name, 0) {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(name, "/users/") || strings.HasPrefix(name, "/classrooms/") {
		// Old original-site bookmarks land on the workbench; its UI owns the
		// selected classroom and task in memory instead of path-based routes.
		http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
		return
	}
	if name == "/" {
		name = "/index.html"
	}
	asset, ok := s[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", asset.kind)
	// Filenames are not content hashed. Revalidate on every navigation while
	// allowing a 304 for unchanged files after a release or browser refresh.
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Add("Vary", "Accept-Encoding")
	body, variant := asset.body, "identity"
	if r.Header.Get("Range") == "" && acceptsGzip(r.Header.Get("Accept-Encoding")) {
		body, variant = asset.gzip, "gzip"
		w.Header().Set("Content-Encoding", "gzip")
	}
	w.Header().Set("ETag", `"`+asset.etag+"-"+variant+`"`)
	http.ServeContent(w, r, name, asset.modified, bytes.NewReader(body))
}

func acceptsGzip(value string) bool {
	for _, option := range strings.Split(value, ",") {
		parts := strings.Split(option, ";")
		if !strings.EqualFold(strings.TrimSpace(parts[0]), "gzip") {
			continue
		}
		quality := 1.0
		for _, parameter := range parts[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if ok && strings.EqualFold(key, "q") {
				parsed, err := strconv.ParseFloat(value, 64)
				if err != nil || parsed < 0 || parsed > 1 {
					return false
				}
				quality = parsed
			}
		}
		return quality > 0
	}
	return false
}
