package compat

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

var assetHosts = map[string]bool{
	"www.educoder.net":                             true,
	"www-cdn.educoder.net":                         true,
	"ali-cdn.educoder.net":                         true,
	"data.educoder.net":                            true,
	"public.educoder.net":                          true,
	"prod-attachments.oss-accelerate.aliyuncs.com": true,
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	path := r.URL.Path
	if strings.ContainsAny(path, "\\:") || strings.ContainsRune(path, 0) {
		http.NotFound(w, r)
		return
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." || part == "." {
			http.NotFound(w, r)
			return
		}
	}
	host := "www.educoder.net"
	assetPath := strings.TrimPrefix(path, "/")
	assetRequest := strings.HasPrefix(path, "/_assets/")
	imageAlias := strings.HasPrefix(path, "/images/avatars/")
	if assetRequest {
		parts := strings.SplitN(strings.TrimPrefix(path, "/_assets/"), "/", 2)
		if len(parts) != 2 || !assetHosts[parts[0]] || parts[1] == "" {
			http.NotFound(w, r)
			return
		}
		host, assetPath = parts[0], parts[1]
		if strings.HasPrefix(strings.ToLower(assetPath), "api/") || (restrictedImageHost(host) && !allowedImagePath(host, assetPath)) {
			http.NotFound(w, r)
			return
		}
	}
	if assetPath == "" {
		assetPath = "index.html"
	}
	base := filepath.Join(s.config.StaticDir, host)
	file, ok := localFile(base, assetPath)
	// The published UI joins API-relative avatars with the same origin. Match
	// those URLs to downloaded image bytes, without sending a network request.
	if imageAlias {
		for _, imageHost := range []string{"ali-cdn.educoder.net", "data.educoder.net", "public.educoder.net"} {
			if candidate, exists := localFile(filepath.Join(s.config.StaticDir, imageHost), assetPath); exists {
				file, ok = candidate, true
				break
			}
		}
	}
	if !ok && !assetRequest && spaRoute(path) {
		file, ok = localFile(base, "index.html")
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	if imageAlias || restrictedImageHost(host) || strings.HasPrefix(assetPath, "images/") {
		contentType, valid := localImageType(file)
		if !valid {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
	}
	s.serveStaticFile(w, r, file)
}

func localFile(base, path string) (string, bool) {
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", false
	}
	candidate := filepath.Join(baseAbs, filepath.FromSlash(path))
	rel, err := filepath.Rel(baseAbs, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	// Resolving symlinks prevents a static symlink from escaping its host tree.
	realBase, err := filepath.EvalSymlinks(baseAbs)
	if err != nil {
		return "", false
	}
	realFile, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", false
	}
	rel, err = filepath.Rel(realBase, realFile)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	info, err := os.Stat(realFile)
	return realFile, err == nil && info.Mode().IsRegular()
}

func spaRoute(path string) bool {
	if filepath.Ext(path) != "" {
		return false
	}
	if path == "/" || path == "/login" || path == "/user/login" || path == "/user/login/" {
		return true
	}
	for _, prefix := range []string{"/users/", "/classrooms/", "/tasks/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
