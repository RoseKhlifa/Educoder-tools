// Package buildinfo exposes the release version embedded from the repository's
// VERSION file, so binaries and go run share the same release identity.
package buildinfo

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var versionFile string

// Commit is set by release builds with -ldflags "-X educoder-cli.Commit=<sha>".
// Development builds retain an explicit, non-secret placeholder.
var Commit = "unknown"

type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

func Current() string { return strings.TrimSpace(versionFile) }

func Release() Info { return Info{Version: Current(), Commit: Commit} }
