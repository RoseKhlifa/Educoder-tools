package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
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
		assets["/"+name] = staticAsset{body: body, kind: kind, modified: info.ModTime()}
	}
	load("index.html", "text/html; charset=utf-8")
	load("styles.css", "text/css; charset=utf-8")
	// Brand artwork is an explicit public asset, not a general assets directory.
	// Unrelated files (including accidental recordings or credentials) stay private.
	load("assets/brand-logo.png", "image/png")
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
	// Version the complete module graph, not only index.html's entry point.
	// A dependency change must change its URL even when an intermediary cache
	// retains an older unversioned jobs.js/api.js. One hash for the original
	// graph also handles cycles without recursively hashing rewritten imports.
	moduleNames := make([]string, 0, len(assets))
	for name := range assets {
		if publicJS(strings.TrimPrefix(name, "/")) {
			moduleNames = append(moduleNames, name)
		}
	}
	sort.Strings(moduleNames)
	graph := sha256.New()
	for _, name := range moduleNames {
		_, _ = fmt.Fprintf(graph, "%d:%s:%d:", len(name), name, len(assets[name].body))
		_, _ = graph.Write(assets[name].body)
	}
	revision := fmt.Sprintf("%x", graph.Sum(nil))[:16]
	for name, asset := range assets {
		if publicJS(strings.TrimPrefix(name, "/")) {
			asset.body = versionModuleImports(name, asset.body, assets, revision)
			// The representation also depends on other files. Its own mtime
			// cannot validate it after a dependency-only update; use the strong
			// representation ETag instead of If-Modified-Since.
			asset.modified = time.Time{}
		}
		var compressed bytes.Buffer
		zw := gzip.NewWriter(&compressed)
		_, _ = zw.Write(asset.body)
		_ = zw.Close()
		asset.gzip = compressed.Bytes()
		asset.etag = fmt.Sprintf("%x", sha256.Sum256(asset.body))
		assets[name] = asset
	}
	return assets
}

// moduleToken records source offsets so rewriting changes only literal module
// specifiers, never surrounding formatting, comments, or ordinary strings.
type moduleToken struct {
	text       string
	start, end int
	quoted     bool
}

func moduleTokens(body []byte) []moduleToken {
	var tokens []moduleToken
	for i := 0; i < len(body); {
		start := i
		switch body[i] {
		case ' ', '\t', '\r', '\n':
			i++
			continue
		case '/':
			if i+1 < len(body) && body[i+1] == '/' {
				for i < len(body) && body[i] != '\n' {
					i++
				}
				continue
			}
			if i+1 < len(body) && body[i+1] == '*' {
				i += 2
				for i+1 < len(body) && !(body[i] == '*' && body[i+1] == '/') {
					i++
				}
				i = min(i+2, len(body))
				continue
			}
			// Skip regex literals in expression-start positions. In particular,
			// /import('./x.js')/ must not become an actual module dependency.
			previous := ""
			if len(tokens) > 0 {
				previous = tokens[len(tokens)-1].text
			}
			if previous == "" || strings.Contains("=([{,:;!?", previous) || previous == "return" || previous == "throw" || previous == "case" || previous == ">" {
				i++
				inClass := false
				for i < len(body) && body[i] != '\n' && body[i] != '\r' {
					if body[i] == '\\' {
						i = min(i+2, len(body))
						continue
					}
					if body[i] == '[' {
						inClass = true
					} else if body[i] == ']' {
						inClass = false
					} else if body[i] == '/' && !inClass {
						i++
						break
					}
					i++
				}
				tokens = append(tokens, moduleToken{text: "/regex/", start: start, end: i})
				continue
			}
		case '\'', '"', '`':
			quote := body[i]
			i++
			for i < len(body) {
				if body[i] == '\\' {
					i = min(i+2, len(body))
					continue
				}
				if body[i] == quote {
					i++
					break
				}
				i++
			}
			tokens = append(tokens, moduleToken{text: string(body[start:i]), start: start, end: i, quoted: quote != '`'})
			continue
		}
		if body[i] == '_' || body[i] == '$' || body[i] >= 'a' && body[i] <= 'z' || body[i] >= 'A' && body[i] <= 'Z' || body[i] >= '0' && body[i] <= '9' {
			for i < len(body) && (body[i] == '_' || body[i] == '$' || body[i] >= 'a' && body[i] <= 'z' || body[i] >= 'A' && body[i] <= 'Z' || body[i] >= '0' && body[i] <= '9') {
				i++
			}
		} else {
			i++
		}
		tokens = append(tokens, moduleToken{text: string(body[start:i]), start: start, end: i})
	}
	return tokens
}

func versionModuleImports(name string, body []byte, assets staticHandler, revision string) []byte {
	tokens := moduleTokens(body)
	specifiers := make(map[int]bool)
	for i, token := range tokens {
		if (token.text != "import" && token.text != "export") || i+1 >= len(tokens) || i > 0 && tokens[i-1].text == "." {
			continue
		}
		next := i + 1
		if token.text == "import" && tokens[next].quoted {
			specifiers[next] = true // side-effect import
			continue
		}
		if token.text == "import" && tokens[next].text == "(" {
			if next+2 < len(tokens) && tokens[next+1].quoted && (tokens[next+2].text == ")" || tokens[next+2].text == ",") {
				specifiers[next+1] = true // literal dynamic import
			}
			continue
		}
		if token.text == "export" && tokens[next].text != "{" && tokens[next].text != "*" {
			continue
		}
		depth := 0
		for j := next; j+1 < len(tokens); j++ {
			switch tokens[j].text {
			case "{":
				depth++
			case "}":
				depth--
			}
			if depth == 0 && tokens[j].text == "from" && tokens[j+1].quoted {
				specifiers[j+1] = true
				break
			}
			if depth < 0 || depth == 0 && (tokens[j].text == ";" || tokens[j].text == "(" || tokens[j].text == "." || tokens[j].quoted) {
				break
			}
		}
	}
	var rewritten bytes.Buffer
	last := 0
	for i, token := range tokens {
		if !specifiers[i] || len(token.text) < 2 || token.text[len(token.text)-1] != token.text[0] {
			continue
		}
		specifier := token.text[1 : len(token.text)-1]
		if strings.ContainsRune(specifier, '\\') || !(strings.HasPrefix(specifier, "./") || strings.HasPrefix(specifier, "../") || strings.HasPrefix(specifier, "/")) {
			continue
		}
		locator, err := url.Parse(specifier)
		if err != nil || locator.Scheme != "" || locator.Host != "" || locator.User != nil || !strings.HasSuffix(locator.Path, ".js") {
			continue
		}
		resolved := locator.Path
		if !strings.HasPrefix(resolved, "/") {
			resolved = path.Join(path.Dir(name), resolved)
		}
		if _, ok := assets[path.Clean(resolved)]; !ok || !publicJS(strings.TrimPrefix(path.Clean(resolved), "/")) {
			continue
		}
		// Keep existing parameters and fragments byte-for-byte. The reserved
		// cache key is appended without changing application query semantics.
		fragment := ""
		if index := strings.IndexByte(specifier, '#'); index >= 0 {
			fragment, specifier = specifier[index:], specifier[:index]
		}
		separator := "?"
		if strings.Contains(specifier, "?") {
			separator = "&"
		}
		rewritten.Write(body[last : token.start+1])
		rewritten.WriteString(specifier + separator + "__asset=" + revision + fragment)
		last = token.end - 1
	}
	if last == 0 {
		return body
	}
	rewritten.Write(body[last:])
	return rewritten.Bytes()
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
