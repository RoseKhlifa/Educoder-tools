package compat

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalImagesUseRealContentTypeAndCanonicalRoutes(t *testing.T) {
	f := newCompatFixture(t)
	var encoded bytes.Buffer
	pixel := image.NewRGBA(image.Rect(0, 0, 1, 1))
	pixel.Set(0, 0, color.RGBA{R: 120, G: 30, B: 70, A: 255})
	if err := jpeg.Encode(&encoded, pixel, nil); err != nil {
		t.Fatal(err)
	}
	jpegBytes := encoded.Bytes()
	for path, content := range map[string][]byte{
		"ali-cdn.educoder.net/images/avatars/User/a": jpegBytes,
		"data.educoder.net/images/avatars/User/b":    jpegBytes,
		"public.educoder.net/PortalImage/banner":     jpegBytes,
		"public.educoder.net/HomeDiscipline/course":  []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"></svg>`),
		"data.educoder.net/images/not-an-image":      []byte("<html>upstream error</html>"),
		"data.educoder.net/api/private.json":         []byte(`{"token":"private"}`),
		"ali-cdn.educoder.net/api/private.json":      []byte(`{"token":"private"}`),
		"public.educoder.net/other/banner":           jpegBytes,
		"prod-attachments.oss-accelerate.aliyuncs.com/2024/06/3e612f197b3294f809afa34c748d83a8_1719380870470.png": jpegBytes,
		"prod-attachments.oss-accelerate.aliyuncs.com/2024/06/private.png":                                        jpegBytes,
	} {
		target := filepath.Join(f.server.config.StaticDir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		path   string
		status int
		kind   string
	}{
		{"/images/avatars/User/a?t=123", 200, "image/jpeg"},
		{"/images/avatars/User/b", 200, "image/jpeg"},
		{"/_assets/ali-cdn.educoder.net/images/avatars/User/a", 200, "image/jpeg"},
		{"/_assets/data.educoder.net/images/avatars/User/b?t=456", 200, "image/jpeg"},
		{"/_assets/public.educoder.net/PortalImage/banner", 200, "image/jpeg"},
		{"/_assets/public.educoder.net/HomeDiscipline/course", 200, "image/svg+xml"},
		{"/_assets/data.educoder.net/images/not-an-image", 404, ""},
		{"/_assets/data.educoder.net/api/private.json", 404, ""},
		{"/_assets/ali-cdn.educoder.net/api/private.json", 404, ""},
		{"/_assets/public.educoder.net/other/banner", 404, ""},
		{"/_assets/prod-attachments.oss-accelerate.aliyuncs.com/2024/06/3e612f197b3294f809afa34c748d83a8_1719380870470.png", 200, "image/jpeg"},
		{"/_assets/prod-attachments.oss-accelerate.aliyuncs.com/2024/06/private.png", 404, ""},
		{"/images/avatars/User/missing", 200, "image/jpeg"},
		{"/_assets/public.educoder.net/PortalImage/missing", 404, ""},
		{"/_assets/unknown.educoder.net/images/avatars/User/a", 404, ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			response := f.request(http.MethodGet, tc.path, "")
			if response.Code != tc.status {
				t.Fatalf("status=%d, want=%d; body=%s", response.Code, tc.status, response.Body.String())
			}
			if tc.kind != "" && response.Header().Get("Content-Type") != tc.kind {
				t.Errorf("Content-Type=%q, want=%q", response.Header().Get("Content-Type"), tc.kind)
			}
			if tc.kind == "image/jpeg" && !bytes.Equal(response.Body.Bytes(), jpegBytes) {
				t.Error("served image bytes changed")
			}
		})
	}
	response := f.request(http.MethodHead, "/images/avatars/User/a", "")
	if response.Code != http.StatusOK || response.Body.Len() != 0 || response.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("HEAD image: status=%d type=%q bytes=%d", response.Code, response.Header().Get("Content-Type"), response.Body.Len())
	}
	if len(f.requests()) != 0 {
		t.Fatal("local image handling performed an upstream API request")
	}
}
