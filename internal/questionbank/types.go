package questionbank

import (
	"encoding/json"
	"errors"
	"time"

	"educoder-cli/internal/problem"
)

var ErrNotFound = errors.New("question bank record not found")
var ErrBudgetExceeded = errors.New("model budget exceeded")

type Ref struct {
	RevisionHash string `json:"revision_hash"`
	ContractHash string `json:"contract_hash"`
}

type ProblemRef = Ref

type CandidateInput struct {
	Files             []problem.SolutionFile `json:"files"`
	Source            string                 `json:"source"`
	Provider          string                 `json:"provider,omitempty"`
	Model             string                 `json:"model,omitempty"`
	PromptVersion     string                 `json:"prompt_version,omitempty"`
	ParentCandidateID int64                  `json:"parent_candidate_id,omitempty"`
}

type Candidate struct {
	ID int64 `json:"id"`
	Ref
	CandidateInput
	FilesHash   string       `json:"files_hash"`
	Status      string       `json:"status"`
	CreatedAt   time.Time    `json:"created_at"`
	Validations []Validation `json:"validations,omitempty"`
}

type Validation struct {
	ID                 int64     `json:"id"`
	CandidateID        int64     `json:"candidate_id"`
	Kind               string    `json:"kind"`
	Result             string    `json:"result"`
	FilesHash          string    `json:"files_hash"`
	SavedFilesHash     string    `json:"saved_files_hash,omitempty"`
	SubmittedFilesHash string    `json:"submitted_files_hash,omitempty"`
	RevisionHash       string    `json:"revision_hash"`
	ContractHash       string    `json:"contract_hash"`
	Feedback           string    `json:"feedback,omitempty"`
	RunID              string    `json:"run_id,omitempty"`
	Terminal           bool      `json:"terminal"`
	Complete           bool      `json:"complete"`
	TestCount          int       `json:"test_count"`
	PassedCount        int       `json:"passed_count"`
	CreatedAt          time.Time `json:"created_at"`
}

// RunRecord is local orchestration state, separate from the reusable bank. Data
// must be a public progress DTO: persistence rejects credential-bearing keys.
type RunRecord struct {
	ID             string          `json:"id"`
	Owner          string          `json:"owner"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	Status         string          `json:"status"`
	Data           json.RawMessage `json:"data"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type Attempt struct {
	ID          int64     `json:"id"`
	RunID       string    `json:"run_id"`
	CandidateID int64     `json:"candidate_id"`
	Number      int       `json:"number"`
	Stage       string    `json:"stage"`
	Result      string    `json:"result"`
	Feedback    string    `json:"feedback"`
	CreatedAt   time.Time `json:"created_at"`
}

// Monetary values are integer micro-US dollars, never floating point. Unknown
// provider outcomes retain their reservation until explicitly reconciled.
type Usage struct {
	ID                int64     `json:"id"`
	RunID             string    `json:"run_id"`
	CandidateID       int64     `json:"candidate_id,omitempty"`
	Provider          string    `json:"provider"`
	Model             string    `json:"model"`
	PricingVersion    string    `json:"pricing_version"`
	InputTokens       int64     `json:"input_tokens"`
	CachedInputTokens int64     `json:"cached_input_tokens"`
	OutputTokens      int64     `json:"output_tokens"`
	ReservedMicroUSD  int64     `json:"reserved_micro_usd"`
	CostMicroUSD      int64     `json:"cost_micro_usd"`
	Status            string    `json:"status"`
	FinishReason      string    `json:"finish_reason"`
	LatencyMillis     int64     `json:"latency_millis"`
	CreatedAt         time.Time `json:"created_at"`
}
