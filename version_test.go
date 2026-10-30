package buildinfo

import (
	"os"
	"strings"
	"testing"
)

func TestCurrentUsesRepositoryVersion(t *testing.T) {
	raw, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := Current(), strings.TrimSpace(string(raw)); got == "" || got != want {
		t.Errorf("embedded version=%q want repository version=%q", got, want)
	}
	if release := Release(); release.Version != Current() || release.Commit != Commit {
		t.Errorf("inconsistent release metadata: %+v", release)
	}
}
