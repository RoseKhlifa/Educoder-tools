package web

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// deployBash finds a POSIX bash able to run the installer's shell helpers.
// On Windows that is Git Bash, located from the git executable; the
// System32 bash.exe is the WSL launcher and must never be used.
func deployBash(t *testing.T) (bash string, path string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		bash, err := exec.LookPath("bash")
		if err != nil {
			t.Skip("bash not found")
		}
		return bash, os.Getenv("PATH")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git for Windows not found")
	}
	// git.exe lives in <root>\cmd or <root>\mingw64\bin.
	dir := filepath.Dir(git)
	for range 3 {
		usrBin := filepath.Join(dir, "usr", "bin")
		if _, err := os.Stat(filepath.Join(usrBin, "bash.exe")); err == nil {
			return filepath.Join(usrBin, "bash.exe"), usrBin + string(os.PathListSeparator) + os.Getenv("PATH")
		}
		dir = filepath.Dir(dir)
	}
	t.Skip("Git Bash not found next to " + git)
	return "", ""
}

// The installer prunes old release trees and pre-deploy backups after a
// verified deploy. It orders them by the version or timestamp in the name,
// keeps the newest ones and the release being installed, and never touches
// anything outside its exact name patterns.
func TestInstallerRetentionKeepsRecentAndCurrentArtifacts(t *testing.T) {
	bash, path := deployBash(t)
	script, err := os.ReadFile("../../deploy/install-release.sh")
	if err != nil {
		t.Fatal(err)
	}
	fn := regexp.MustCompile(`(?ms)^prune_artifacts\(\) \{.*?^\}`).Find(script)
	if fn == nil {
		t.Fatal("prune_artifacts not found in install-release.sh")
	}
	root := t.TempDir()
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	// Modification times run opposite to the names, so ordering by mtime
	// would keep exactly the wrong artifacts.
	mkdir := func(rel string, hour int) {
		dir := filepath.Join(root, rel)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		stamp := base.Add(time.Duration(hour) * time.Hour)
		if err := os.Chtimes(dir, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	// Releases 0.1.1..0.1.15; 0.1.3 is the one being installed. 0.1.9 vs
	// 0.1.10 also rules out a lexical sort.
	for i := 1; i <= 15; i++ {
		mkdir(filepath.Join("releases", "0.1."+strconv.Itoa(i)), 100-i)
	}
	mkdir(filepath.Join("releases", "v0.1.56-external-import"), 0)
	if err := os.WriteFile(filepath.Join(root, "releases", "release-v0.1.56.tar.gz"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 25; i++ {
		mkdir(filepath.Join("backups", fmt.Sprintf("pre-v0.1.%d-202611%02dT000000Z", i, i)), 100-i)
	}
	// A rollback backup: newest timestamp, old version. And the reverse.
	mkdir(filepath.Join("backups", "pre-v0.1.2-20261130T000000Z"), 0)
	mkdir(filepath.Join("backups", "pre-v0.1.30-20261031T000000Z"), 200)
	mkdir(filepath.Join("backups", "manual-keep"), 0)
	if err := os.WriteFile(filepath.Join(root, "fn.sh"), fn, 0o644); err != nil {
		t.Fatal(err)
	}
	slash := filepath.ToSlash
	cmd := exec.Command(bash, "-c", `set -euo pipefail; source "$ROOT/fn.sh"; prune_artifacts`)
	cmd.Env = append(os.Environ(), "PATH="+path, "ROOT="+slash(root), "EDUCODER_TOOLS_ROOT="+slash(root),
		"EDUCODER_TOOLS_SKIP_IMAGE_PRUNE=1", "release_root="+slash(filepath.Join(root, "releases", "0.1.3")), "version=0.1.3")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("prune_artifacts: %v\n%s", err, out)
	}
	names := func(dir string) []string {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range entries {
			out = append(out, e.Name())
		}
		return out
	}
	releases := names("releases")
	for _, keep := range []string{"0.1.3", "0.1.6", "0.1.11", "0.1.15", "v0.1.56-external-import", "release-v0.1.56.tar.gz"} {
		if !slices.Contains(releases, keep) {
			t.Errorf("release %s was removed: %v", keep, releases)
		}
	}
	for _, gone := range []string{"0.1.1", "0.1.2", "0.1.4", "0.1.5"} {
		if slices.Contains(releases, gone) {
			t.Errorf("release %s was kept beyond the bound: %v", gone, releases)
		}
	}
	if len(releases) != 13 {
		t.Errorf("releases kept=%d, want 10 newest + current + 2 unrelated: %v", len(releases), releases)
	}
	backups := names("backups")
	count := 0
	for _, b := range backups {
		if strings.HasPrefix(b, "pre-v") {
			count++
		}
	}
	if count != 20 {
		t.Errorf("backups kept=%d, want the 20 newest: %v", count, backups)
	}
	for _, keep := range []string{"manual-keep", "pre-v0.1.2-20261130T000000Z", "pre-v0.1.25-20261125T000000Z", "pre-v0.1.7-20261107T000000Z"} {
		if !slices.Contains(backups, keep) {
			t.Errorf("backup %s was removed: %v", keep, backups)
		}
	}
	for _, gone := range []string{"pre-v0.1.30-20261031T000000Z", "pre-v0.1.6-20261106T000000Z", "pre-v0.1.1-20261101T000000Z"} {
		if slices.Contains(backups, gone) {
			t.Errorf("backup %s was kept beyond the bound: %v", gone, backups)
		}
	}
}
