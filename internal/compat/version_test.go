package compat

import (
	"encoding/json"
	"net/http"
	"testing"

	buildinfo "educoder-cli"
)

func TestRuntimeReleaseMetadata(t *testing.T) {
	f := newCompatFixture(t)
	for _, path := range []string{"/healthz", "/version"} {
		t.Run(path, func(t *testing.T) {
			w := f.request(http.MethodGet, path, "")
			var got struct {
				Status  string `json:"status"`
				Version string `json:"version"`
				Commit  string `json:"commit"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusOK || got.Version != buildinfo.Current() || got.Commit != buildinfo.Commit || w.Header().Get("X-App-Version") != got.Version {
				t.Errorf("unexpected release response: status=%d headers=%v body=%s", w.Code, w.Header(), w.Body.String())
			}
			if path == "/healthz" && got.Status != "ok" {
				t.Errorf("health status changed: %q", got.Status)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Error("release metadata must not be cached")
			}
			invalid := f.request(http.MethodPost, path, "")
			if invalid.Code != http.StatusMethodNotAllowed || invalid.Header().Get("Allow") != http.MethodGet {
				t.Errorf("metadata method restrictions: status=%d headers=%v", invalid.Code, invalid.Header())
			}
		})
	}
	if len(f.requests()) != 0 {
		t.Fatal("release metadata reached upstream")
	}
	if got := f.request(http.MethodGet, "/missing.js", "").Header().Get("X-App-Version"); got != buildinfo.Current() {
		t.Errorf("static errors must include version: %q", got)
	}
}

func TestDefaultConfigPublicPortAndOverride(t *testing.T) {
	if got := DefaultConfig().Addr; got != "0.0.0.0:11454" {
		t.Fatalf("default deployment listener: %q", got)
	}
	t.Setenv("REUSE_ADDR", "127.0.0.1:12345")
	cfg, err := LoadConfig()
	if err != nil || cfg.Addr != "127.0.0.1:12345" {
		t.Fatalf("explicit REUSE_ADDR override: config=%+v err=%v", cfg, err)
	}
}
