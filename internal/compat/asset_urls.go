package compat

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// localizeImageURLs uses only verified local image files. API paths, arbitrary
// remote hosts and missing resources retain their original response values.
// Nothing is downloaded or forwarded while serving a browser request.
func (s *Server) localizeImageURLs(raw []byte) ([]byte, error) {
	var body any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&body); err != nil {
		return nil, err
	}
	var walk func(any) any
	walk = func(value any) any {
		switch current := value.(type) {
		case map[string]any:
			for key, child := range current {
				if key == "public_bucket_host" {
					if origin, ok := child.(string); ok && strings.TrimRight(origin, "/") == "https://data.educoder.net" {
						current[key] = "/_assets/data.educoder.net"
						continue
					}
				}
				current[key] = walk(child)
			}
		case []any:
			for index, child := range current {
				current[index] = walk(child)
			}
		case string:
			return s.localImageURL(current)
		}
		return value
	}
	return json.Marshal(walk(body))
}

func (s *Server) localImageURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.Port() != "" {
		return value
	}
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "data.educoder.net", "public.educoder.net", "ali-cdn.educoder.net", "www-cdn.educoder.net", "prod-attachments.oss-accelerate.aliyuncs.com":
	default:
		return value
	}
	if strings.HasPrefix(parsed.Path, "/api/") || strings.Contains(parsed.Path, "\\") {
		return value
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return value
		}
	}
	// User avatars are session-independent public resources loaded on demand by
	// the dedicated avatar handler; they must not require a checked-in file.
	if host == "data.educoder.net" || host == "ali-cdn.educoder.net" || host == "public.educoder.net" {
		if key, matched := matchAvatarPath(parsed.Path); matched && validAvatarKey(key) {
			query := parsed.Query()
			parsed.Scheme, parsed.Host, parsed.RawPath, parsed.Fragment = "", "", "", ""
			parsed.RawQuery = ""
			if timestamp := query.Get("t"); timestamp != "" && validAvatarQuery("t="+url.QueryEscape(timestamp)) {
				parsed.RawQuery = "t=" + url.QueryEscape(timestamp)
			}
			return parsed.String()
		}
	}
	file, ok := localFile(filepath.Join(s.config.StaticDir, host), strings.TrimPrefix(parsed.Path, "/"))
	if !ok || !localImageFile(file) {
		return value
	}
	parsed.Scheme, parsed.Host = "", ""
	parsed.Path = "/_assets/" + host + parsed.Path
	parsed.RawPath = ""
	return parsed.String()
}

func localImageFile(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	header := make([]byte, 512)
	size, _ := io.ReadFull(file, header)
	header = header[:size]
	if strings.HasPrefix(http.DetectContentType(header), "image/") {
		return true
	}
	// SVG bytes are XML/text according to net/http's generic content sniffer.
	trimmed := bytes.TrimSpace(bytes.TrimPrefix(header, []byte{0xef, 0xbb, 0xbf}))
	return bytes.HasPrefix(trimmed, []byte("<svg")) || bytes.HasPrefix(trimmed, []byte("<?xml")) && bytes.Contains(trimmed, []byte("<svg"))
}
