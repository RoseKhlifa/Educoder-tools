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
	ActualStdout   string `json:"actual_stdout,omitempty"`
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
	Problem  problem.Snapshot
	Previous *Candidate
	Feedback []Feedback
	Profile  Profile
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

// Error deliberately contains no provider body, prompt, key, or request URL.
// Generate returns any known charged usage alongside an error. BilledUnknown
// means the caller must retain its reservation rather than assume zero cost.
type Error struct {
	Kind          string
	HTTPStatus    int
	Retryable     bool
	BilledUnknown bool
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
	case "truncated_output", "invalid_output_json", "candidate_contract", "objective_candidate_contract":
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
	if e.HTTPStatus != 0 {
		return fmt.Sprintf("model provider %s (HTTP %d)", e.Kind, e.HTTPStatus)
	}
	return "model provider " + e.Kind
}
