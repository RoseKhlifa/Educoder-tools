package compat

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadedImageURLsLocalizeWithoutChangingAPIAndMissingImages(t *testing.T) {
	root := t.TempDir()
	image, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jO1sAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"data.educoder.net/images/avatars/LaboratorySetting/1nav", "public.educoder.net/PortalImage/42"} {
		file := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, image, 0644); err != nil {
			t.Fatal(err)
		}
	}
	s := NewServer(Config{StaticDir: root})
	raw := []byte(`{"setting":{"public_bucket_host":"https://data.educoder.net"},"logo":"https://data.educoder.net/images/avatars/LaboratorySetting/1nav?t=123","banners":["https://public.educoder.net/PortalImage/42"],"missing":"https://public.educoder.net/PortalImage/999","api":"https://data.educoder.net/api/home/index.json","foreign":"https://unrelated.example/image.png","id":9007199254740993}`)
	modified, err := s.localizeImageURLs(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`"public_bucket_host":"/_assets/data.educoder.net"`,
		`"logo":"/_assets/data.educoder.net/images/avatars/LaboratorySetting/1nav?t=123"`,
		`"banners":["/_assets/public.educoder.net/PortalImage/42"]`,
		`"missing":"https://public.educoder.net/PortalImage/999"`,
		`"api":"https://data.educoder.net/api/home/index.json"`,
		`"foreign":"https://unrelated.example/image.png"`,
		`"id":9007199254740993`,
	} {
		if !strings.Contains(string(modified), expected) {
			t.Errorf("missing %s in %s", expected, modified)
		}
	}
	if !json.Valid(modified) {
		t.Fatal("invalid JSON after image URL conversion")
	}
	for _, value := range []string{"https://public.educoder.net/../PortalImage/42", "https://public.educoder.net:8443/PortalImage/42", "https://user@public.educoder.net/PortalImage/42"} {
		if got := s.localImageURL(value); got != value {
			t.Errorf("unexpected URL rewrite: %s", got)
		}
	}
}

func TestOfficialAvatarURLsUseDedicatedLocalRouteWithoutDownloadedFiles(t *testing.T) {
	s := NewServer(Config{StaticDir: t.TempDir()})
	for _, host := range []string{"data.educoder.net", "ali-cdn.educoder.net", "public.educoder.net"} {
		for _, tc := range []struct{ input, expected string }{
			{"/images/avatars/User/b", "/images/avatars/User/b"},
			{"/images/avatars/User/avatar_12-Ab?t=123", "/images/avatars/User/avatar_12-Ab?t=123"},
			{"/images/avatars/User/avatar_12-Ab?t=123&unused=secret", "/images/avatars/User/avatar_12-Ab?t=123"},
		} {
			if got := s.localImageURL("https://" + host + tc.input); got != tc.expected {
				t.Errorf("%s%s -> %q, want %q", host, tc.input, got, tc.expected)
			}
		}
	}
	// Relative avatar keys remain compatible with the original UI's IMG_SERVER
	// concatenation. Unsupported hosts and malformed keys do not gain a route.
	for _, value := range []string{"avatars/User/b", "https://unrelated.example/images/avatars/User/b", "https://data.educoder.net/images/avatars/User/../private", "https://data.educoder.net/images/avatars/User/"} {
		if got := s.localImageURL(value); got != value {
			t.Errorf("unexpected rewrite %q -> %q", value, got)
		}
	}
}
