package problem

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const maxContentBytes = 4 << 20

var credentialPattern = regexp.MustCompile(`(?i)(?:\b(?:authorization|cookie|set-cookie)\s*:\s*[^\s]+|\b(?:sec_key|access_token|refresh_token)\s*[=:]\s*["']?[a-z0-9_./+\-=]{6,}|\b(?:api_key|api-key)\s*[=:]\s*["']?[a-z0-9_./+\-=]{12,}|\bsk-[a-z0-9]{16,})`)
var urlPattern = regexp.MustCompile(`(?i)(?:https?|wss?)://[^\s<>"'\x60]+`)

// ValidatePublicText rejects recognizable credentials and temporary signed URLs
// at the boundary rather than persisting them and trying to redact them later.
// Ordinary source identifiers such as api_key remain valid without a value.
func ValidatePublicText(s string) error {
	if !utf8.ValidString(s) || len(s) > maxContentBytes || strings.IndexByte(s, 0) >= 0 {
		return errors.New("public content is invalid UTF-8, too large, or contains NUL")
	}
	if credentialPattern.MatchString(s) {
		return errors.New("public content contains credential material")
	}
	for _, raw := range urlPattern.FindAllString(s, -1) {
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		if u.User != nil || u.Scheme == "ws" || u.Scheme == "wss" {
			return errors.New("public content contains a runtime or authenticated URL")
		}
		for key := range u.Query() {
			k := strings.ToLower(key)
			if strings.Contains(k, "signature") || strings.Contains(k, "credential") || strings.Contains(k, "token") || k == "sec_key" || k == "sign" || k == "expires" || strings.HasPrefix(k, "x-amz-") || strings.HasPrefix(k, "x-oss-") {
				return errors.New("public content contains a signed resource URL")
			}
		}
	}
	return nil
}

func HashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func normalize(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }

func canonicalHash(v any) string {
	b, _ := json.Marshal(v)
	return HashContent(string(b))
}

func validPath(p string) bool {
	return p != "" && !strings.ContainsAny(p, "\\\x00") && !strings.HasPrefix(p, "/") && !strings.Contains(p, ":") && path.Clean(p) == p && p != "." && p != ".." && !strings.HasPrefix(p, "../")
}

// Prepare validates a snapshot and calculates versioned fingerprints. It copies
// slices so normalization never mutates the caller's current editor contents.
// Only CRLF is normalized for semantic fingerprints; source hashes remain exact.
func Prepare(in Snapshot) (Snapshot, error) {
	s := in
	if s.SchemaVersion == 0 {
		s.SchemaVersion = SchemaVersion
	}
	if s.SchemaVersion != SchemaVersion {
		return Snapshot{}, errors.New("unsupported problem schema")
	}
	if s.Kind == "" {
		s.Kind = "programming"
	}
	if s.Kind != "programming" {
		return Snapshot{}, errors.New("unsupported problem kind")
	}
	if len(s.Files) == 0 || len(s.Files) > 64 {
		return Snapshot{}, errors.New("problem must contain 1 to 64 files")
	}
	s.Files = append([]File(nil), in.Files...)
	s.PublicTests = append([]PublicTest(nil), in.PublicTests...)
	s.ObservedConstraints = append([]string(nil), in.ObservedConstraints...)
	s.EnvironmentContract = append([]string(nil), in.EnvironmentContract...)
	texts := []string{s.Title, s.StatementMarkdown, s.EditMode, s.SnapshotCompleteness}
	texts = append(texts, s.ObservedConstraints...)
	texts = append(texts, s.EnvironmentContract...)
	for i := range s.Files {
		f := &s.Files[i]
		if !validPath(f.Path) {
			return Snapshot{}, errors.New("invalid problem file path")
		}
		if f.Filename != "" && f.Filename != path.Base(f.Path) {
			return Snapshot{}, errors.New("filename does not match file path")
		}
		f.Filename = path.Base(f.Path)
		if f.Extension != "" && f.Extension != path.Ext(f.Path) {
			return Snapshot{}, errors.New("extension does not match file path")
		}
		f.Extension = path.Ext(f.Path)
		if f.Language == "" {
			return Snapshot{}, errors.New("problem file language is required")
		}
		f.CurrentContentHash = HashContent(f.CurrentContent)
		if f.StarterProvenance == "" {
			f.StarterProvenance = "unknown"
		}
		if f.StarterContent != nil && f.StarterProvenance != "confirmed_template" && f.StarterProvenance != "verified_template" {
			return Snapshot{}, errors.New("starter content requires confirmed template provenance")
		}
		if f.StarterContent != nil {
			v := *f.StarterContent
			f.StarterContent = &v
			texts = append(texts, v)
		}
		texts = append(texts, f.Path, f.Language, f.CurrentContent, f.StarterProvenance, f.EntrySignature, f.FixedRegionHash)
	}
	sort.Slice(s.Files, func(i, j int) bool { return s.Files[i].Path < s.Files[j].Path })
	for i := 1; i < len(s.Files); i++ {
		if s.Files[i-1].Path == s.Files[i].Path {
			return Snapshot{}, errors.New("duplicate problem file path")
		}
	}
	for _, t := range s.PublicTests {
		texts = append(texts, t.Stdin, t.ExpectedStdout)
	}
	for _, text := range texts {
		if err := ValidatePublicText(text); err != nil {
			return Snapshot{}, err
		}
	}
	tests := append([]PublicTest(nil), s.PublicTests...)
	for i := range tests {
		tests[i].Stdin = normalize(tests[i].Stdin)
		tests[i].ExpectedStdout = normalize(tests[i].ExpectedStdout)
	}
	constraints := append([]string{}, s.ObservedConstraints...)
	for i := range constraints {
		constraints[i] = normalize(constraints[i])
	}
	sort.Strings(constraints)
	s.RevisionHash = canonicalHash(struct {
		Version                int `json:"v"`
		Kind, Title, Statement string
		Tests                  []PublicTest
		Constraints            []string
	}{SchemaVersion, s.Kind, normalize(s.Title), normalize(s.StatementMarkdown), tests, constraints})
	type contractFile struct{ Path, Language, Extension, EntrySignature, FixedRegionHash, TemplateHash string }
	files := make([]contractFile, 0, len(s.Files))
	for _, f := range s.Files {
		templateHash := "unknown"
		if f.StarterContent != nil {
			templateHash = HashContent(normalize(*f.StarterContent))
		}
		files = append(files, contractFile{f.Path, f.Language, f.Extension, normalize(f.EntrySignature), f.FixedRegionHash, templateHash})
	}
	env := append([]string{}, s.EnvironmentContract...)
	for i := range env {
		env[i] = normalize(env[i])
	}
	sort.Strings(env)
	editMode := s.EditMode
	if editMode == "" {
		editMode = "unknown"
	}
	s.ContractHash = canonicalHash(struct {
		Version     int
		Files       []contractFile
		Environment []string
		EditMode    string
	}{SchemaVersion, files, env, editMode})
	return s, nil
}

// Validate checks the snapshot boundary without requiring callers to remember
// to use the fingerprinting helper. It deliberately returns no normalized copy;
// use Prepare when hashes and derived fields are needed.
func (s Snapshot) Validate() error {
	_, err := Prepare(s)
	return err
}

// HashFiles binds a candidate to complete, byte-exact sources and exact paths.
func HashFiles(in []SolutionFile) (string, error) {
	if len(in) == 0 || len(in) > 64 {
		return "", errors.New("candidate must contain 1 to 64 files")
	}
	files := append([]SolutionFile(nil), in...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	type fileHash struct{ Path, Hash string }
	hashes := make([]fileHash, 0, len(files))
	for i, f := range files {
		if !validPath(f.Path) {
			return "", errors.New("invalid candidate path")
		}
		if i > 0 && files[i-1].Path == f.Path {
			return "", errors.New("duplicate candidate path")
		}
		if err := ValidatePublicText(f.Content); err != nil {
			return "", fmt.Errorf("candidate content: %w", err)
		}
		hashes = append(hashes, fileHash{f.Path, HashContent(f.Content)})
	}
	return canonicalHash(hashes), nil
}
