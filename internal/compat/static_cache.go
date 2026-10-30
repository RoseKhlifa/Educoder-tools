package compat

import (
	"bytes"
	"compress/gzip"
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

const maxCompressedStaticSize = 16 << 20

// staticAssetCache stores compressed public build files, never API responses or
// user avatars. Original bytes stay on disk; the total compressed payload is
// bounded, and simultaneous requests for one file share the compression work.
type staticAssetCache struct {
	mu       sync.Mutex
	limit    int64
	used     int64
	entries  map[string]*list.Element
	order    *list.List
	inflight map[string]*staticAssetFlight
}

type staticAsset struct {
	key         string
	etag        string
	gzipETag    string
	contentType string
	compressed  []byte
	cost        int64
}

type staticAssetFlight struct {
	done  chan struct{}
	asset *staticAsset
	err   error
}

func newStaticAssetCache(limit int64) *staticAssetCache {
	return &staticAssetCache{
		limit: limit, entries: make(map[string]*list.Element),
		order: list.New(), inflight: make(map[string]*staticAssetFlight),
	}
}

func staticAssetKey(path string, info os.FileInfo) string {
	return fmt.Sprintf("%s\x00%d\x00%d", path, info.Size(), info.ModTime().UnixNano())
}

func (c *staticAssetCache) get(path string, info os.FileInfo) (*staticAsset, error) {
	key := staticAssetKey(path, info)
	c.mu.Lock()
	if element := c.entries[key]; element != nil {
		c.order.MoveToFront(element)
		asset := element.Value.(*staticAsset)
		c.mu.Unlock()
		return asset, nil
	}
	if flight := c.inflight[key]; flight != nil {
		c.mu.Unlock()
		<-flight.done
		return flight.asset, flight.err
	}
	flight := &staticAssetFlight{done: make(chan struct{})}
	c.inflight[key] = flight
	c.mu.Unlock()

	asset, err := buildStaticAsset(path, info)
	c.mu.Lock()
	if err == nil && asset.cost <= c.limit {
		// Account for keys and metadata as well as payload bytes, so a large
		// directory of tiny files also has a bounded cache footprint.
		for c.used+asset.cost > c.limit && c.order.Len() > 0 {
			last := c.order.Back()
			old := last.Value.(*staticAsset)
			delete(c.entries, old.key)
			c.used -= old.cost
			c.order.Remove(last)
		}
		c.entries[key] = c.order.PushFront(asset)
		c.used += asset.cost
	}
	flight.asset, flight.err = asset, err
	delete(c.inflight, key)
	close(flight.done)
	c.mu.Unlock()
	return asset, err
}

func buildStaticAsset(path string, info os.FileInfo) (*staticAsset, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	asset := &staticAsset{key: staticAssetKey(path, info)}
	asset.contentType = staticContentType(path)
	if asset.contentType == "" {
		prefix := make([]byte, 512)
		n, readErr := file.Read(prefix)
		if readErr != nil && readErr != io.EOF {
			return nil, readErr
		}
		asset.contentType = http.DetectContentType(prefix[:n])
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
	}

	if info.Size() > maxCompressedStaticSize {
		// Do not buffer or scan large downloads on the request path. This weak
		// validator is suitable for revalidation, while ServeContent still
		// supports identity byte ranges and modification-date If-Range.
		asset.etag = fmt.Sprintf(`W/"%x-%x"`, info.ModTime().UnixNano(), info.Size())
	} else if compressibleStaticType(asset.contentType) && info.Size() >= 256 {
		original, readErr := io.ReadAll(io.LimitReader(file, maxCompressedStaticSize+1))
		if readErr != nil {
			return nil, readErr
		}
		digest := sha256.Sum256(original)
		asset.etag = `"` + hex.EncodeToString(digest[:]) + `"`
		if len(original) <= maxCompressedStaticSize {
			var output bytes.Buffer
			compressor := gzip.NewWriter(&output)
			if _, err = compressor.Write(original); err != nil {
				return nil, err
			}
			if err = compressor.Close(); err != nil {
				return nil, err
			}
			if output.Len() < len(original) {
				asset.compressed = output.Bytes()
				compressedDigest := sha256.Sum256(asset.compressed)
				asset.gzipETag = `"` + hex.EncodeToString(compressedDigest[:]) + `"`
			}
		}
	} else {
		hash := sha256.New()
		if _, err = io.Copy(hash, file); err != nil {
			return nil, err
		}
		asset.etag = `"` + hex.EncodeToString(hash.Sum(nil)) + `"`
	}
	asset.cost = int64(len(asset.compressed) + len(asset.key) + len(asset.contentType) + 256)
	return asset, nil
}

func compressibleStaticType(contentType string) bool {
	kind := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	return strings.HasPrefix(kind, "text/") || kind == "application/javascript" ||
		kind == "application/json" || kind == "application/xml" ||
		kind == "application/wasm" || kind == "image/svg+xml"
}

func staticContentType(path string) string {
	// Keep the build's common types independent from host MIME registries.
	// In particular, Windows may not have a WOFF2 entry at all.
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".json", ".map":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".wasm":
		return "application/wasm"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	case ".eot":
		return "application/vnd.ms-fontobject"
	default:
		return mime.TypeByExtension(ext)
	}
}

// Explicit encoding weights take precedence over a wildcard. In particular,
// gzip;q=0 must never receive compressed bytes, even when *;q=1 is present.
func acceptsGzip(value string) bool {
	wildcard, explicit := false, false
	allowed := false
	for _, item := range strings.Split(value, ",") {
		parts := strings.Split(item, ";")
		name := strings.TrimSpace(strings.ToLower(parts[0]))
		if name != "gzip" && name != "*" {
			continue
		}
		quality := 1.0
		for _, parameter := range parts[1:] {
			pair := strings.SplitN(strings.TrimSpace(parameter), "=", 2)
			if len(pair) == 2 && strings.EqualFold(strings.TrimSpace(pair[0]), "q") {
				parsed, err := strconv.ParseFloat(strings.TrimSpace(pair[1]), 64)
				if err != nil || parsed < 0 || parsed > 1 {
					quality = 0
				} else {
					quality = parsed
				}
			}
		}
		if name == "gzip" {
			explicit, allowed = true, quality > 0
		} else {
			wildcard = quality > 0
		}
	}
	return (explicit && allowed) || (!explicit && wildcard)
}

var hashedStaticName = regexp.MustCompile(`(?i)(?:^|[.\-_])[0-9a-f]{8,64}(?:[.\-_]|$)`)

func staticCacheControl(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".html" || ext == ".htm" {
		return "no-cache"
	}
	if hashedStaticName.MatchString(filepath.Base(path)) {
		return "public, max-age=31536000, immutable"
	}
	return "public, max-age=3600, must-revalidate"
}

func (s *Server) serveStaticFile(w http.ResponseWriter, r *http.Request, path string) {
	file, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	var asset *staticAsset
	if s.staticCache != nil {
		asset, err = s.staticCache.get(path, info)
	} else {
		asset, err = buildStaticAsset(path, info)
	}
	if err != nil {
		http.Error(w, "static resource unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", staticCacheControl(path))
	w.Header().Set("ETag", asset.etag)
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", asset.contentType)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if len(asset.compressed) != 0 {
		w.Header().Add("Vary", "Accept-Encoding")
		// Ranges refer to original file offsets. ServeContent handles identity
		// ranges, If-Range, HEAD, If-None-Match and modification dates.
		if r.Header.Get("Range") == "" && acceptsGzip(r.Header.Get("Accept-Encoding")) {
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Content-Length", strconv.Itoa(len(asset.compressed)))
			w.Header().Set("ETag", asset.gzipETag)
			http.ServeContent(w, r, filepath.Base(path), info.ModTime(), bytes.NewReader(asset.compressed))
			return
		}
	}
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
}
