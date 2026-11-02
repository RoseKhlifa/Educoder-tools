package questionbank

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"educoder-cli/internal/problem"
)

// External records are reference material, never evidence that an answer passed
// a current upstream task. Binding creates an ordinary unverified candidate.
const externalSchema = `
CREATE TABLE IF NOT EXISTS external_entries (
 id INTEGER PRIMARY KEY,
 repository TEXT NOT NULL, source_commit TEXT NOT NULL, source_path TEXT NOT NULL,
 source_sha256 TEXT NOT NULL, entry_sha256 TEXT NOT NULL,
 experiment TEXT NOT NULL, title TEXT NOT NULL, position INTEGER NOT NULL DEFAULT 0,
 kind TEXT NOT NULL, status TEXT NOT NULL, reason TEXT NOT NULL,
 files_json TEXT NOT NULL, reference_json TEXT NOT NULL, extension TEXT NOT NULL,
 created_at TEXT NOT NULL,
 UNIQUE(repository, source_commit, source_path)
);
CREATE INDEX IF NOT EXISTS external_identity_idx ON external_entries(experiment,title,kind,status);
CREATE TABLE IF NOT EXISTS external_candidate_bindings (
 candidate_id INTEGER NOT NULL REFERENCES solutions(id),
 external_entry_id INTEGER NOT NULL REFERENCES external_entries(id),
 created_at TEXT NOT NULL,
 PRIMARY KEY(candidate_id,external_entry_id)
);`

type ExternalIdentity struct {
	Experiment string `json:"experiment"`
	Title      string `json:"title"`
	Position   int    `json:"position,omitempty"`
}

type ExternalEntry struct {
	ID int64 `json:"id"`
	ExternalIdentity
	Repository   string                 `json:"repository"`
	Commit       string                 `json:"commit"`
	SourcePath   string                 `json:"source_path"`
	SourceSHA256 string                 `json:"source_sha256"`
	EntrySHA256  string                 `json:"entry_sha256"`
	Kind         string                 `json:"kind"`
	Status       string                 `json:"status"`
	Reason       string                 `json:"reason"`
	Files        []problem.SolutionFile `json:"files,omitempty"`
	Reference    json.RawMessage        `json:"reference,omitempty"`
	Extension    string                 `json:"extension,omitempty"`
}

func normalizeExternalIdentity(s string) string { return strings.Join(strings.Fields(s), " ") }

// External text may come from a Windows checkout with core.autocrlf enabled.
// The browser editor saves LF text. Normalize before creating the candidate,
// leaving the source record and its provenance hashes byte-exact. Evaluation
// still verifies the exact bytes of this new candidate, never an equivalent hash.
func externalExecutionContent(kind, content string) string {
	if kind == "programming" {
		return strings.ReplaceAll(content, "\r\n", "\n")
	}
	return content
}

func scanExternal(row interface{ Scan(...any) error }) (ExternalEntry, error) {
	var e ExternalEntry
	var files, reference string
	err := row.Scan(&e.ID, &e.Repository, &e.Commit, &e.SourcePath, &e.SourceSHA256, &e.EntrySHA256,
		&e.Experiment, &e.Title, &e.Position, &e.Kind, &e.Status, &e.Reason, &files, &reference, &e.Extension)
	if err != nil {
		return e, err
	}
	if err = json.Unmarshal([]byte(files), &e.Files); err != nil {
		return e, err
	}
	e.Reference = json.RawMessage(reference)
	return e, nil
}

const externalColumns = `id,repository,source_commit,source_path,source_sha256,entry_sha256,experiment,title,position,kind,status,reason,files_json,reference_json,extension`

// ExternalEntries is an administrator/import inspection boundary. No source
// accounts, runtime instance identifiers, or upstream metadata are stored.
func (s *Store) ExternalEntries(ctx context.Context) ([]ExternalEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+externalColumns+` FROM external_entries ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExternalEntry
	for rows.Next() {
		e, err := scanExternal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// BindExternalCandidates requires an exact normalized experiment/title match and
// a compatible current file contract. It does not weaken snapshot fingerprints.
// Distinct contents matching the same identity are ambiguous and remain references.
func (s *Store) BindExternalCandidates(ctx context.Context, ref Ref, identity ExternalIdentity, snapshot problem.Snapshot) ([]Candidate, error) {
	if len(snapshot.Files) != 1 || snapshot.Kind != "programming" && snapshot.Kind != "" {
		return nil, nil
	}
	kind := "programming"
	if snapshot.EditMode == "graphic" {
		if !strings.EqualFold(path.Ext(snapshot.Files[0].Path), ".mdj") || snapshot.Files[0].Language != "json" {
			return nil, nil
		}
		staruml := false
		for _, env := range snapshot.EnvironmentContract {
			if env == "graphic:staruml" {
				staruml = true
			}
		}
		if !staruml {
			return nil, nil
		}
		kind = "diagram"
	}
	identity.Experiment = normalizeExternalIdentity(identity.Experiment)
	identity.Title = normalizeExternalIdentity(identity.Title)
	if identity.Experiment == "" || identity.Title == "" {
		return nil, nil
	}
	prepared, err := problem.Prepare(snapshot)
	if err != nil {
		return nil, err
	}
	if prepared.RevisionHash != ref.RevisionHash || prepared.ContractHash != ref.ContractHash {
		return nil, errors.New("external candidate snapshot does not match reference")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+externalColumns+` FROM external_entries WHERE experiment=? AND title=? AND kind=? AND status='candidate' ORDER BY id`, identity.Experiment, identity.Title, kind)
	if err != nil {
		return nil, err
	}
	var matches []ExternalEntry
	for rows.Next() {
		e, scanErr := scanExternal(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		if e.Position > 0 && identity.Position > 0 && e.Position != identity.Position {
			continue
		}
		if len(e.Files) != 1 {
			continue
		}
		file := e.Files[0]
		if file.Path != "" && file.Path != prepared.Files[0].Path {
			continue
		}
		if file.Path == "" && (e.Extension == "" || !strings.EqualFold(e.Extension, path.Ext(prepared.Files[0].Path))) {
			continue
		}
		matches = append(matches, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(matches) == 0 {
		return nil, err
	}
	content := externalExecutionContent(kind, matches[0].Files[0].Content)
	for _, e := range matches[1:] {
		if externalExecutionContent(kind, e.Files[0].Content) != content {
			return nil, nil
		}
	}
	e := matches[0]
	candidate, err := s.AddCandidate(ctx, ref, CandidateInput{Files: []problem.SolutionFile{{Path: prepared.Files[0].Path, Content: content}}, Source: fmt.Sprintf("external:%s@%s:%d", e.Repository, e.Commit, e.ID)})
	if err != nil {
		return nil, err
	}
	// Bind every identical provenance record so repeat imports cannot lose attribution.
	for _, e := range matches {
		if _, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO external_candidate_bindings(candidate_id,external_entry_id,created_at) VALUES(?,?,?)`, candidate.ID, e.ID, now()); err != nil {
			return nil, err
		}
	}
	return []Candidate{candidate}, nil
}

// IsExternalCandidate checks durable provenance and the executable content,
// rather than trusting a caller-controlled Source label. Pre-normalization
// candidates remain in history but must not be selected ahead of LF candidates.
func (s *Store) IsExternalCandidate(ctx context.Context, candidateID int64) (bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.kind,e.files_json FROM external_candidate_bindings b JOIN external_entries e ON e.id=b.external_entry_id WHERE b.candidate_id=? AND e.kind IN ('programming','diagram') AND e.status='candidate'`, candidateID)
	if err != nil {
		return false, err
	}
	type boundSource struct {
		kind  string
		files []problem.SolutionFile
	}
	var sources []boundSource
	for rows.Next() {
		var kind, raw string
		if err = rows.Scan(&kind, &raw); err != nil {
			rows.Close()
			return false, err
		}
		var files []problem.SolutionFile
		if err = json.Unmarshal([]byte(raw), &files); err != nil {
			rows.Close()
			return false, err
		}
		sources = append(sources, boundSource{kind, files})
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(sources) == 0 {
		return false, err
	}
	candidate, err := s.Candidate(ctx, candidateID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(candidate.Files) != 1 {
		return false, nil
	}
	for _, source := range sources {
		if len(source.files) == 1 && (source.files[0].Path == "" || source.files[0].Path == candidate.Files[0].Path) && externalExecutionContent(source.kind, source.files[0].Content) == candidate.Files[0].Content {
			return true, nil
		}
	}
	return false, nil
}
