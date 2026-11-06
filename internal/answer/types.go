// Package answer generates and validates candidate source files. It accepts
// public problem snapshots, never upstream clients or account execution state.
package answer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"educoder-cli/internal/problem"
)

type File = problem.SolutionFile

type Candidate struct {
	Kind     string                 `json:"kind"`
	Language string                 `json:"language"`
	Files    []problem.SolutionFile `json:"files"`
}

// Feedback is data from an observed attempt, not an instruction or HTTP trace.
type Feedback struct {
	Kind           string `json:"kind"`
	Message        string `json:"message,omitempty"`
	Stdin          string `json:"stdin,omitempty"`
	ExpectedStdout string `json:"expected_stdout,omitempty"`
	ActualStdout   string `json:"actual_stdout"`
}

// RepairAttempt binds observed feedback to the exact source that produced it.
// Candidate may be omitted from older entries to bound prompt size; the most
// recent complete candidate is also supplied through Request.Previous.
type RepairAttempt struct {
	FilesHash string     `json:"files_hash"`
	Candidate *Candidate `json:"candidate,omitempty"`
	Feedback  []Feedback `json:"feedback"`
}

type Profile struct {
	Model           string `json:"model"`
	Thinking        string `json:"thinking"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	MaxTokens       int    `json:"max_tokens"`
}

func DefaultProfile() Profile {
	return Profile{Model: "deepseek-flash", Thinking: "disabled", MaxTokens: 4096}
}

func RepairProfile() Profile {
	return Profile{Model: "deepseek-flash", Thinking: "enabled", ReasoningEffort: "low", MaxTokens: 4096}
}

func ProProfile() Profile {
	return Profile{Model: "deepseek-v4-pro", Thinking: "enabled", ReasoningEffort: "low", MaxTokens: 4096}
}

type Request struct {
	Problem       problem.Snapshot
	Previous      *Candidate
	Feedback      []Feedback
	RepairHistory []RepairAttempt
	Profile       Profile
}

type Usage struct {
	Known            bool  `json:"known"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	CacheHitTokens   int64 `json:"cache_hit_tokens"`
	CacheMissTokens  int64 `json:"cache_miss_tokens"`
	ReasoningTokens  int64 `json:"reasoning_tokens"`
}

type Result struct {
	Candidate         Candidate     `json:"candidate"`
	Usage             Usage         `json:"usage"`
	ProviderRequestID string        `json:"provider_request_id,omitempty"`
	FinishReason      string        `json:"finish_reason,omitempty"`
	Model             string        `json:"model"`
	Duration          time.Duration `json:"duration"`
	CostMicroUSD      int64         `json:"cost_micro_usd"`
	CostKnown         bool          `json:"cost_known"`
}

// Rates are integer micro-US dollars per million tokens. Reasoning tokens are
// already part of completion_tokens and must not be billed for a second time.
type Pricing struct {
	InputMicroUSDPerMillion    int64  `json:"input_micro_usd_per_million"`
	CacheHitMicroUSDPerMillion int64  `json:"cache_hit_micro_usd_per_million"`
	OutputMicroUSDPerMillion   int64  `json:"output_micro_usd_per_million"`
	Version                    string `json:"version"`
}

type Estimate struct {
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	CostMicroUSD int64  `json:"cost_micro_usd"`
	Model        string `json:"model"`
	PriceVersion string `json:"price_version"`
}

type Provider interface {
	Estimate(Request) (Estimate, error)
	Generate(context.Context, Request) (Result, error)
}

type Config struct {
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
	Timeout    time.Duration
	Prices     map[string]Pricing
	// ImageTokenReserve is the conservative input-token reservation per image.
	// Zero selects 8192. Charged usage always comes from the provider response.
	ImageTokenReserve int64
}

// ObjectiveOutputReason is a fixed output-contract classification, never a
// provider response fragment or a candidate answer.
type ObjectiveOutputReason string

const (
	ObjectiveOutputKind            ObjectiveOutputReason = "kind"
	ObjectiveOutputAnswerCount     ObjectiveOutputReason = "answer_count"
	ObjectiveOutputAnswerShape     ObjectiveOutputReason = "answer_shape"
	ObjectiveOutputUnknownOption   ObjectiveOutputReason = "unknown_option"
	ObjectiveOutputDuplicateOption ObjectiveOutputReason = "duplicate_option"
	ObjectiveOutputOptionOrder     ObjectiveOutputReason = "option_order"
)

type ObjectiveAnswerShape string

const (
	ObjectiveShapeSingle   ObjectiveAnswerShape = "single"
	ObjectiveShapeMultiple ObjectiveAnswerShape = "multiple"
)

// ObjectiveOutputViolation retains only structural metadata. In particular it
// must never contain the submitted/returned labels, question text, raw JSON,
// prior scores or provider body. Rendering also validates both string enums.
type ObjectiveOutputViolation struct {
	Reason           ObjectiveOutputReason
	ExpectedAnswers  int
	ReceivedAnswers  int
	QuestionPosition int
	Shape            ObjectiveAnswerShape
	OptionCount      int
}

// Error deliberately contains no provider body, prompt, key, or request URL.
// Generate returns any known charged usage alongside an error. BilledUnknown
// means the caller must retain its reservation rather than assume zero cost.
type Error struct {
	Kind          string
	HTTPStatus    int
	Retryable     bool
	BilledUnknown bool
	// RetryAfterSeconds is sanitized and capped; the original header is never
	// retained in diagnostics or persisted run records.
	RetryAfterSeconds int
	// Response metadata contains only fixed classifications and byte counts,
	// never the response body or the underlying transport error's text.
	ResponsePhase string
	ReadCause     string
	BytesRead     int64
	ExpectedBytes int64
	// ObjectiveOutput is optional, content-free evidence for correcting an
	// objective response's shape without exposing earlier answers or scores.
	ObjectiveOutput *ObjectiveOutputViolation
}

func sanitizedObjectiveOutputViolation(e *Error) (ObjectiveOutputViolation, bool) {
	if e == nil || e.Kind != "objective_candidate_contract" || e.ObjectiveOutput == nil {
		return ObjectiveOutputViolation{}, false
	}
	v := *e.ObjectiveOutput
	switch v.Reason {
	case ObjectiveOutputKind, ObjectiveOutputAnswerCount, ObjectiveOutputAnswerShape,
		ObjectiveOutputUnknownOption, ObjectiveOutputDuplicateOption, ObjectiveOutputOptionOrder:
	default:
		return ObjectiveOutputViolation{}, false
	}
	if v.ExpectedAnswers < 0 {
		v.ExpectedAnswers = 0
	}
	if v.ReceivedAnswers < 0 {
		v.ReceivedAnswers = 0
	}
	if v.QuestionPosition < 0 {
		v.QuestionPosition = 0
	}
	if v.OptionCount < 0 {
		v.OptionCount = 0
	}
	switch v.Shape {
	case ObjectiveShapeSingle, ObjectiveShapeMultiple:
	default:
		v.Shape = ""
	}
	return v, true
}

// ObjectiveOutputFeedback returns only structural correction instructions. It
// does not reuse Error.Error() or any provider text, so a clean independent
// review stays independent of previous candidate content and correctness.
func ObjectiveOutputFeedback(err error) (Feedback, bool) {
	var providerErr *Error
	if !errors.As(err, &providerErr) {
		return Feedback{}, false
	}
	v, ok := sanitizedObjectiveOutputViolation(providerErr)
	if !ok {
		return Feedback{}, false
	}
	message := "Correct only the objective JSON output contract. Return one JSON object with kind=objective and an answers array; do not return explanations or other fields."
	if v.ExpectedAnswers > 0 {
		message += fmt.Sprintf(" The answers array must have exactly %d entries, one per entry in questions in its supplied order; the rejected output had %d entries. context_questions are reference context only and do not request additional answers.", v.ExpectedAnswers, v.ReceivedAnswers)
	}
	if v.QuestionPosition > 0 {
		message += fmt.Sprintf(" The structural violation concerns the requested question at original position %d.", v.QuestionPosition)
	}
	switch v.Reason {
	case ObjectiveOutputKind:
		message += " Use the literal kind value objective."
	case ObjectiveOutputAnswerCount:
		message += " Answer only the requested questions; do not combine answers into one entry or include sibling-context entries."
	case ObjectiveOutputAnswerShape:
		message += " Every answers entry must be a non-empty string containing only allowed uppercase option labels."
	case ObjectiveOutputUnknownOption:
		message += " Use only option labels present in the requested question."
	case ObjectiveOutputDuplicateOption:
		message += " Do not repeat an option label within an answer."
	case ObjectiveOutputOptionOrder:
		message += " Put selected option labels in alphabetical order without separators."
	}
	switch v.Shape {
	case ObjectiveShapeSingle:
		message += " This is single choice: return exactly one allowed uppercase option label."
	case ObjectiveShapeMultiple:
		message += " This is multiple choice: return one or more distinct allowed uppercase option labels concatenated in alphabetical order, without separators."
	default:
		message += " For single choice use exactly one allowed uppercase option label; for multiple choice concatenate distinct allowed uppercase option labels in alphabetical order without separators."
	}
	if v.OptionCount > 0 {
		message += fmt.Sprintf(" The requested question contains %d allowed options; use its supplied labels, not labels inferred from other questions.", v.OptionCount)
	}
	return Feedback{Kind: "objective_output_contract", Message: message}, true
}

// IsRetryableError classifies provider-only failures. Callers must separately
// check their mutation boundary and remaining context, cost, and attempt
// budgets. It never authorizes replaying an uncertain platform operation.
func IsRetryableError(err error) bool {
	var providerErr *Error
	if !errors.As(err, &providerErr) {
		return false
	}
	if providerErr.Retryable {
		return true
	}
	switch providerErr.Kind {
	case "truncated_output", "invalid_output_json", "candidate_contract", "objective_candidate_contract", "objective_fixed_answers_mismatch":
		return true
	default:
		return false
	}
}

// IsTruncatedOutput lets orchestration reserve a complete output budget and
// disable optional reasoning for its next bounded provider attempt.
func IsTruncatedOutput(err error) bool {
	var providerErr *Error
	return errors.As(err, &providerErr) && providerErr.Kind == "truncated_output"
}

func (e *Error) Error() string {
	message := "model provider " + e.Kind
	if e.HTTPStatus != 0 {
		message = fmt.Sprintf("%s (HTTP %d)", message, e.HTTPStatus)
	}
	if e.ResponsePhase == "body" {
		cause := e.ReadCause
		switch cause {
		case "deadline", "canceled", "unexpected_eof", "read":
		default:
			cause = "read"
		}
		bytesRead, expectedBytes := e.BytesRead, e.ExpectedBytes
		if bytesRead < 0 {
			bytesRead = 0
		}
		if expectedBytes < -1 {
			expectedBytes = -1
		}
		message += fmt.Sprintf(" [phase=body cause=%s bytes_read=%d expected_bytes=%d]", cause, bytesRead, expectedBytes)
	}
	if v, ok := sanitizedObjectiveOutputViolation(e); ok {
		shape := string(v.Shape)
		if shape == "" {
			shape = "unspecified"
		}
		message += fmt.Sprintf(" [objective_output reason=%s expected_answers=%d received_answers=%d position=%d shape=%s option_count=%d]", v.Reason, v.ExpectedAnswers, v.ReceivedAnswers, v.QuestionPosition, shape, v.OptionCount)
	}
	return message
}
