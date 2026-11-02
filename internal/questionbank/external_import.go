package questionbank

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"educoder-cli/internal/problem"
)

const maxExternalFile = 4 << 20
const maxExternalInventory = 10000

var externalCommitPattern = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)

type ExternalImportOptions struct {
	SourceDir  string
	Repository string
	Commit     string
	DryRun     bool
}

type ExternalImportReport struct {
	Repository string         `json:"repository"`
	Commit     string         `json:"commit"`
	DryRun     bool           `json:"dry_run"`
	Total      int            `json:"total"`
	Inserted   int            `json:"inserted"`
	Existing   int            `json:"existing"`
	Usable     int            `json:"usable_candidates"`
	Reference  int            `json:"reference_only"`
	Rejected   int            `json:"rejected"`
	Kinds      map[string]int `json:"kinds"`
	Reasons    map[string]int `json:"reasons"`
}

type externalReader struct {
	root  string
	total int64
}

func cleanExternalPath(p string) bool {
	return p != "" && !strings.ContainsAny(p, "\\\x00:") && !strings.HasPrefix(p, "/") && path.Clean(p) == p && p != "." && p != ".." && !strings.HasPrefix(p, "../")
}

func (r *externalReader) read(rel string, limit int64) ([]byte, error) {
	if !cleanExternalPath(rel) {
		return nil, errors.New("invalid source path")
	}
	current := r.root
	for _, component := range strings.Split(rel, "/") {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("symbolic link source is not permitted")
		}
	}
	f, err := os.Open(current)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("source is not a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("source file exceeds import limit")
	}
	r.total += int64(len(b))
	if r.total > 128<<20 {
		return nil, errors.New("source inventory exceeds import limit")
	}
	if !utf8.Valid(b) || strings.IndexByte(string(b), 0) >= 0 {
		return nil, errors.New("source is not UTF-8 text")
	}
	return b, nil
}

func canonicalExternalRepository(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("repository must be a public HTTPS GitHub repository URL")
	}
	p := strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), ".git")
	if len(strings.Split(strings.TrimPrefix(p, "/"), "/")) != 2 || !cleanExternalPath(strings.TrimPrefix(p, "/")) {
		return "", errors.New("invalid repository URL")
	}
	return "https://github.com" + p, nil
}

// ImportExternalDirectory only reads the solutions inventory and answers.json.
// It never executes upstream code or reads failed attempts/session directories.
func (s *Store) ImportExternalDirectory(ctx context.Context, opts ExternalImportOptions) (ExternalImportReport, error) {
	repo, err := canonicalExternalRepository(opts.Repository)
	if err != nil {
		return ExternalImportReport{}, err
	}
	if !externalCommitPattern.MatchString(opts.Commit) {
		return ExternalImportReport{}, errors.New("commit must be a full 40-character SHA")
	}
	opts.Commit = strings.ToLower(opts.Commit)
	report := ExternalImportReport{Repository: repo, Commit: opts.Commit, DryRun: opts.DryRun, Kinds: map[string]int{}, Reasons: map[string]int{}}
	root, err := filepath.Abs(opts.SourceDir)
	if err != nil {
		return report, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return report, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return report, errors.New("source directory must be a real directory")
	}
	// A checkout supplies a verifiable commit identity. Exported snapshots are
	// also supported, with exact per-entry hashes and caller-supplied commit.
	if _, gitErr := os.Lstat(filepath.Join(root, ".git")); gitErr == nil {
		remoteCommand := exec.CommandContext(ctx, "git", "-C", root, "remote", "get-url", "origin")
		remote, remoteErr := remoteCommand.Output()
		remoteRepository, canonicalErr := canonicalExternalRepository(strings.TrimSpace(string(remote)))
		if remoteErr != nil || canonicalErr != nil || remoteRepository != repo {
			return report, errors.New("source checkout origin does not match requested repository")
		}
		cmd := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "HEAD")
		actual, revErr := cmd.Output()
		if revErr != nil || strings.TrimSpace(string(actual)) != opts.Commit {
			return report, errors.New("source checkout HEAD does not match requested commit")
		}
		// Modified or additional public source files would falsely attribute
		// content to the requested revision, even when their hashes are recorded.
		cmd = exec.CommandContext(ctx, "git", "-C", root, "status", "--porcelain", "--untracked-files=all", "--", "solutions", "answers.json")
		changes, statusErr := cmd.Output()
		if statusErr != nil || len(strings.TrimSpace(string(changes))) != 0 {
			return report, errors.New("source checkout contains modified or untracked import files")
		}
	} else if !os.IsNotExist(gitErr) {
		return report, gitErr
	}
	r := externalReader{root: root}
	var entries []ExternalEntry
	metas, err := filepath.Glob(filepath.Join(root, "solutions", "*", "*", "meta.json"))
	if err != nil {
		return report, err
	}
	sort.Strings(metas)
	if len(metas) > maxExternalInventory {
		return report, errors.New("source inventory exceeds entry limit")
	}
	for _, metaPath := range metas {
		if err = ctx.Err(); err != nil {
			return report, err
		}
		rel, _ := filepath.Rel(root, metaPath)
		rel = filepath.ToSlash(rel)
		e := ExternalEntry{Repository: repo, Commit: opts.Commit, SourcePath: rel, Kind: "reference", Status: "rejected", Reason: "invalid_metadata", Reference: json.RawMessage(`null`)}
		b, readErr := r.read(rel, 1<<20)
		if readErr != nil {
			return report, fmt.Errorf("read %s: %w", rel, readErr)
		}
		e.SourceSHA256 = problem.HashContent(string(b))
		var m struct {
			Experiment string `json:"experiment"`
			Subject    string `json:"subject"`
			Path       string `json:"path"`
			Kind       string `json:"kind"`
			File       string `json:"file"`
			Position   int    `json:"position"`
		}
		if json.Unmarshal(b, &m) != nil {
			entries = append(entries, finalizeExternalEntry(e))
			continue
		}
		e.Experiment = normalizeExternalIdentity(m.Experiment)
		e.Title = normalizeExternalIdentity(m.Subject)
		e.Position = m.Position
		if e.Experiment == "" || e.Title == "" || problem.ValidatePublicText(e.Experiment) != nil || problem.ValidatePublicText(e.Title) != nil || e.Position < 0 {
			e.Experiment = ""
			e.Title = ""
			entries = append(entries, finalizeExternalEntry(e))
			continue
		}
		if !cleanExternalPath(m.File) || strings.Contains(m.File, "/") || m.Path != "" && !cleanExternalPath(m.Path) {
			e.Reason = "invalid_source_path"
			entries = append(entries, finalizeExternalEntry(e))
			continue
		}
		payloadRel := path.Join(path.Dir(rel), m.File)
		payload, payloadErr := r.read(payloadRel, maxExternalFile)
		if payloadErr != nil {
			return report, fmt.Errorf("read %s: %w", payloadRel, payloadErr)
		}
		// Hash the exact metadata bytes and declared payload bytes together; only
		// explicitly allowed public fields are persisted below.
		e.SourceSHA256 = problem.HashContent(string(b) + "\x00" + string(payload))
		e.Extension = strings.ToLower(path.Ext(m.File))
		if err = problem.ValidatePublicText(string(payload)); err != nil {
			e.Reason = "nonpublic_content"
			entries = append(entries, finalizeExternalEntry(e))
			continue
		}
		switch m.Kind {
		case "code":
			e.Kind = "programming"
			e.Files = []problem.SolutionFile{{Path: m.Path, Content: string(payload)}}
			e.Status = "candidate"
			e.Reason = "unverified_external_source"
		case "choice":
			e.Kind = "objective"
			e.Status = "reference"
			e.Reason = "missing_question_and_option_identity"
			var v struct {
				Answer []struct {
					Number   int      `json:"number"`
					Selected []string `json:"selected"`
				} `json:"answer"`
			}
			if json.Unmarshal(payload, &v) != nil {
				e.Status = "rejected"
				e.Reason = "invalid_choice_reference"
			} else {
				e.Reference = json.RawMessage(marshal(v))
			}
		case "uml":
			e.Kind = "diagram"
			e.Status = "reference"
			e.Reason = "diagram_protocol_not_verified"
			e.Files = []problem.SolutionFile{{Path: m.Path, Content: string(payload)}}
			if e.Extension != ".mdj" {
				e.Reason = "diagram_plaintext_reference"
			} else if validExternalDiagram(payload) {
				e.Status = "candidate"
				e.Reason = "unverified_external_source"
			} else {
				e.Status = "rejected"
				e.Reason = "invalid_diagram_project"
			}
		default:
			e.Reason = "unsupported_source_kind"
		}
		entries = append(entries, finalizeExternalEntry(e))
	}
	if _, statErr := os.Lstat(filepath.Join(root, "answers.json")); statErr == nil {
		b, readErr := r.read("answers.json", 16<<20)
		if readErr != nil {
			return report, readErr
		}
		fallback, parseErr := readExternalFallback(repo, opts.Commit, b)
		if parseErr != nil {
			return report, parseErr
		}
		entries = append(entries, fallback...)
	} else if !os.IsNotExist(statErr) {
		return report, statErr
	}
	if len(entries) == 0 {
		return report, errors.New("no supported external source inventory found")
	}
	if len(entries) > maxExternalInventory {
		return report, errors.New("source inventory exceeds entry limit")
	}
	for _, e := range entries {
		if err = ctx.Err(); err != nil {
			return report, err
		}
		report.Total++
		report.Kinds[e.Kind]++
		report.Reasons[e.Reason]++
		switch e.Status {
		case "candidate":
			report.Usable++
		case "reference":
			report.Reference++
		default:
			report.Rejected++
		}
	}
	if opts.DryRun {
		return report, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	for _, e := range entries {
		result, execErr := tx.ExecContext(ctx, `INSERT OR IGNORE INTO external_entries(repository,source_commit,source_path,source_sha256,entry_sha256,experiment,title,position,kind,status,reason,files_json,reference_json,extension,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, e.Repository, e.Commit, e.SourcePath, e.SourceSHA256, e.EntrySHA256, e.Experiment, e.Title, e.Position, e.Kind, e.Status, e.Reason, marshal(e.Files), string(e.Reference), e.Extension, now())
		if execErr != nil {
			return report, execErr
		}
		n, _ := result.RowsAffected()
		if n == 1 {
			report.Inserted++
		} else {
			var digest string
			if err = tx.QueryRowContext(ctx, `SELECT entry_sha256 FROM external_entries WHERE repository=? AND source_commit=? AND source_path=?`, e.Repository, e.Commit, e.SourcePath).Scan(&digest); err != nil {
				return report, err
			}
			if digest != e.EntrySHA256 {
				return report, errors.New("source content changed for pinned import identity")
			}
			report.Existing++
		}
	}
	return report, tx.Commit()
}

func finalizeExternalEntry(e ExternalEntry) ExternalEntry {
	if len(e.Reference) == 0 {
		e.Reference = json.RawMessage(`null`)
	}
	copy := e
	copy.ID = 0
	copy.EntrySHA256 = ""
	e.EntrySHA256 = problem.HashContent(marshal(copy))
	return e
}

func readExternalFallback(repo, commit string, b []byte) ([]ExternalEntry, error) {
	var doc struct {
		Classrooms []struct {
			Experiments []struct {
				Name   string `json:"name"`
				Levels []struct {
					Title    string          `json:"title"`
					Kind     string          `json:"kind"`
					Position int             `json:"position"`
					Answered bool            `json:"answered"`
					Answer   json.RawMessage `json:"answer"`
				} `json:"levels"`
			} `json:"experiments"`
		} `json:"classrooms"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return nil, errors.New("invalid answers.json inventory")
	}
	var out []ExternalEntry
	for ci, c := range doc.Classrooms {
		for ei, experiment := range c.Experiments {
			for li, level := range experiment.Levels {
				// Positional choice references already exist in solutions. Fallback is
				// deliberately code-only; no incomplete option identities are promoted.
				if level.Kind != "code" || !level.Answered {
					continue
				}
				e := ExternalEntry{Repository: repo, Commit: commit, SourcePath: fmt.Sprintf("answers.json#classrooms/%d/experiments/%d/levels/%d", ci, ei, li), SourceSHA256: problem.HashContent(string(b)), ExternalIdentity: ExternalIdentity{Experiment: normalizeExternalIdentity(experiment.Name), Title: normalizeExternalIdentity(level.Title), Position: level.Position}, Kind: "programming", Status: "candidate", Reason: "unverified_external_source", Reference: json.RawMessage(`null`)}
				var answer struct {
					Language string `json:"language"`
					Code     string `json:"code"`
				}
				if json.Unmarshal(level.Answer, &answer) != nil || answer.Code == "" || e.Experiment == "" || e.Title == "" || e.Position < 0 {
					e.Status = "rejected"
					e.Reason = "invalid_code_reference"
					e.Experiment = ""
					e.Title = ""
				} else if problem.ValidatePublicText(answer.Code) != nil || problem.ValidatePublicText(e.Experiment) != nil || problem.ValidatePublicText(e.Title) != nil {
					e.Status = "rejected"
					e.Reason = "nonpublic_content"
					e.Experiment = ""
					e.Title = ""
				} else {
					e.Files = []problem.SolutionFile{{Content: answer.Code}}
					e.Extension = externalLanguageExtension(answer.Language, answer.Code)
					if e.Extension == "" {
						e.Status = "reference"
						e.Reason = "missing_file_contract"
					}
				}
				out = append(out, finalizeExternalEntry(e))
			}
		}
	}
	return out, nil
}

func validExternalDiagram(payload []byte) bool {
	var project struct {
		Type          string            `json:"_type"`
		OwnedElements []json.RawMessage `json:"ownedElements"`
	}
	return json.Unmarshal(payload, &project) == nil && project.Type == "Project" && len(project.OwnedElements) > 0
}

var externalFilenameHint = regexp.MustCompile(`(?m)^\s*(?://|#)\s*([A-Za-z0-9_.-]+\.(?:java|py|sh|cpp|c|h|js|ts|sql))\s*$`)

func externalLanguageExtension(language, code string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "shell", "bash", "sh":
		return ".sh"
	case "python", "python3":
		return ".py"
	case "java":
		return ".java"
	case "c":
		return ".c"
	case "c++", "cpp":
		return ".cpp"
	case "javascript", "js":
		return ".js"
	case "typescript", "ts":
		return ".ts"
	case "sql":
		return ".sql"
	}
	if m := externalFilenameHint.FindStringSubmatch(code); len(m) > 0 {
		return path.Ext(m[1])
	}
	if strings.HasPrefix(code, "#!/bin/bash") || strings.HasPrefix(code, "#!/bin/sh") {
		return ".sh"
	}
	return ""
}
