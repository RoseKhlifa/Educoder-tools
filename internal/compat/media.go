package compat

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const liteMediaPrefix = "/_lite/media-v1/"

// These documents contain no scripts, external images, fonts or remote links.
//
//go:embed media/*.svg media/aliases.json
var liteMediaFiles embed.FS

var decorativeMediaAliases = func() map[string]string {
	raw, err := liteMediaFiles.ReadFile("media/aliases.json")
	if err != nil {
		panic(err)
	}
	aliases := make(map[string]string)
	if err := json.Unmarshal(raw, &aliases); err != nil {
		panic(err)
	}
	return aliases
}()

func liteMediaURL(name string) string { return liteMediaPrefix + name + ".svg" }

// mediaName matches only a versioned embedded document or an explicitly removed
// decorative file. It cannot turn arbitrary task attachments into placeholders.
func mediaName(path string) (name string, matched bool) {
	if strings.ContainsAny(path, "\\:\x00") {
		return "", false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "." || part == ".." {
			return "", false
		}
	}
	if strings.HasPrefix(path, "/_assets/") {
		parts := strings.SplitN(strings.TrimPrefix(path, "/_assets/"), "/", 2)
		if len(parts) != 2 || !assetHosts[parts[0]] {
			return "", false
		}
		// The original build prefixes setting image values with IMG_SERVER.
		candidate := "/" + strings.TrimLeft(parts[1], "/")
		if strings.HasPrefix(candidate, liteMediaPrefix) {
			path = candidate
		}
	}
	if strings.HasPrefix(path, liteMediaPrefix) {
		name = strings.TrimPrefix(path, liteMediaPrefix)
		if strings.Contains(name, "/") || !strings.HasSuffix(name, ".svg") {
			return "", false
		}
		name = strings.TrimSuffix(name, ".svg")
		if _, err := liteMediaFiles.ReadFile("media/" + name + ".svg"); err != nil {
			return "", false
		}
		return name, true
	}
	name, matched = decorativeMediaAliases[path]
	return name, matched
}

func (s *Server) serveLiteMedia(w http.ResponseWriter, r *http.Request) bool {
	name, matched := mediaName(r.URL.Path)
	if !matched {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return true
	}
	body, err := liteMediaFiles.ReadFile("media/" + name + ".svg")
	if err != nil {
		http.NotFound(w, r)
		return true
	}
	digest := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(digest[:]) + `"`
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("ETag", etag)
	if strings.Contains(r.URL.Path, liteMediaPrefix) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	for _, candidate := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		if strings.TrimSpace(candidate) == etag || strings.TrimSpace(candidate) == "W/"+etag || strings.TrimSpace(candidate) == "*" {
			w.WriteHeader(http.StatusNotModified)
			return true
		}
	}
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
	return true
}

// lightweightPublicMedia applies only to observed public presentation routes.
// Classroom, task, editor, person and instructional attachment payloads do not
// enter this transform. The original response's JSON number precision survives.
func lightweightPublicMedia(path string, raw []byte) ([]byte, error) {
	if path != "/api/home/index.json" && path != "/api/setting.json" && path != "/api/popup_windows/get_popup_windows.json" {
		return raw, nil
	}
	var body map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil {
		return nil, err
	}
	switch path {
	case "/api/home/index.json":
		if _, exists := body["images_url"]; exists {
			slides := make([]any, 3)
			for i := range slides {
				slides[i] = map[string]any{"image_url": liteMediaURL(fmt.Sprintf("carousel-%d", i+1)), "path": "/"}
			}
			body["images_url"] = slides
		}
		for _, field := range []string{"new_discipline", "subjects", "shixuns"} {
			replaceHomeCovers(body[field])
		}
		if demos, ok := body["shixun_demo"].([]any); ok {
			for _, demo := range demos {
				if item, ok := demo.(map[string]any); ok {
					item["video"] = ""
					item["poster"] = liteMediaURL("video")
					item["img"] = liteMediaURL("video")
				}
			}
		}
	case "/api/setting.json":
		if setting, ok := body["setting"].(map[string]any); ok {
			for key, value := range setting {
				if _, ok := value.(string); !ok {
					continue
				}
				switch {
				case strings.Contains(key, "banner_url") || strings.HasSuffix(key, "qrcode_url"):
					setting[key] = strings.TrimPrefix(liteMediaURL("banner"), "/")
				case strings.HasSuffix(key, "logo_url") || key == "sci_logo":
					setting[key] = strings.TrimPrefix(liteMediaURL("logo"), "/")
				case key == "footer":
					setting[key] = footerImageSource.ReplaceAllString(value.(string), `${1}`+liteMediaURL("logo")+`${2}`)
				}
			}
			if ad, ok := setting["adv_course"].(map[string]any); ok {
				ad["image_url"] = liteMediaURL("announcement")
				ad["jump_url"] = "/"
				ad["name"] = "广告位打盹中，脑细胞请开工"
			}
		}
	case "/api/popup_windows/get_popup_windows.json":
		if entries, ok := body["data"].([]any); ok {
			for _, entry := range entries {
				if item, ok := entry.(map[string]any); ok {
					item["content"] = []any{"![今日小公告：页面在减肥，知识不缩水](" + liteMediaURL("announcement") + ")"}
					for _, key := range []string{"image_url", "image", "cover_url"} {
						if _, exists := item[key]; exists {
							item[key] = liteMediaURL("announcement")
						}
					}
				}
			}
		}
	}
	return json.Marshal(body)
}

var footerImageSource = regexp.MustCompile(`(?i)(<img\b[^>]*\bsrc\s*=\s*["'])[^"']*(["'])`)

func replaceHomeCovers(value any) {
	switch current := value.(type) {
	case []any:
		for _, child := range current {
			replaceHomeCovers(child)
		}
	case map[string]any:
		for key, child := range current {
			if key == "avatar_url" || strings.Contains(key, "user") || strings.Contains(key, "teacher") {
				continue
			}
			if key == "image_url" || key == "cover" || key == "cover_url" {
				if source, ok := child.(string); ok && !isUserMedia(source) {
					current[key] = liteMediaURL("cover")
					continue
				}
			}
			replaceHomeCovers(child)
		}
	}
}

func isUserMedia(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && strings.Contains("/"+strings.TrimLeft(parsed.Path, "/"), "/avatars/User/")
}
