// Package problem defines the reusable, account-independent problem boundary.
// Runtime clients, sessions, save keys and upstream instance identifiers have no
// representation here and must stay in the execution adapter.
package problem

const SchemaVersion = 1

type File struct {
	Path               string  `json:"path"`
	Filename           string  `json:"filename"`
	Language           string  `json:"language"`
	Extension          string  `json:"extension"`
	CurrentContent     string  `json:"current_content"`
	CurrentContentHash string  `json:"current_content_hash"`
	StarterContent     *string `json:"starter_content,omitempty"`
	StarterProvenance  string  `json:"starter_provenance"`
	EntrySignature     string  `json:"entry_signature,omitempty"`
	FixedRegionHash    string  `json:"fixed_region_hash,omitempty"`
}

type PublicTest struct {
	Stdin          string `json:"stdin"`
	ExpectedStdout string `json:"expected_stdout"`
}

type Snapshot struct {
	SchemaVersion        int          `json:"schema_version"`
	Kind                 string       `json:"kind"`
	Title                string       `json:"title"`
	StatementMarkdown    string       `json:"statement_markdown"`
	Files                []File       `json:"files"`
	PublicTests          []PublicTest `json:"public_tests"`
	ObservedConstraints  []string     `json:"observed_constraints"`
	EnvironmentContract  []string     `json:"environment_contract"`
	EditMode             string       `json:"edit_mode"`
	SnapshotCompleteness string       `json:"snapshot_completeness"`
	RevisionHash         string       `json:"problem_revision_hash"`
	ContractHash         string       `json:"execution_contract_hash"`
}

type ProblemSnapshot = Snapshot

type SolutionFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}
