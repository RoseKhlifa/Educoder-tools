// Package jobs runs account-isolated asynchronous work in the HTTP process.
// Credentials and executable closures remain in memory; only public job state is stored.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const (
	Queued      = "queued"
	Running     = "running"
	Succeeded   = "succeeded"
	Failed      = "failed"
	Canceled    = "canceled"
	Uncertain   = "uncertain"
	Interrupted = "interrupted"
)

// Status-prefixed aliases keep call sites self-documenting when several
// state machines are imported by the web package.
const (
	StatusQueued      = Queued
	StatusRunning     = Running
	StatusSucceeded   = Succeeded
	StatusFailed      = Failed
	StatusCanceled    = Canceled
	StatusUncertain   = Uncertain
	StatusInterrupted = Interrupted
)

type Request struct {
	Course         string `json:"course"`
	HomeworkID     int64  `json:"homework_id"`
	Game           string `json:"game,omitempty"`
	Mode           string `json:"mode"`
	Scope          string `json:"scope,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type Event struct {
	Phase    string         `json:"phase"`
	Kind     string         `json:"kind,omitempty"`
	Message  string         `json:"message"`
	At       time.Time      `json:"at"`
	Progress *BatchProgress `json:"progress,omitempty"`
}

// BatchProgress contains public counters only. Completed includes already-full
// levels skipped by this run; Skipped is a subset of Completed.
type BatchProgress struct {
	TotalHomeworks        int    `json:"total_homeworks,omitempty"`
	CompletedHomeworks    int    `json:"completed_homeworks,omitempty"`
	SkippedHomeworks      int    `json:"skipped_homeworks,omitempty"`
	NeedsServiceHomeworks int    `json:"needs_service_homeworks,omitempty"`
	CurrentCourse         string `json:"current_course,omitempty"`
	CurrentHomeworkID     int64  `json:"current_homework_id,omitempty"`
	CurrentHomeworkTitle  string `json:"current_homework_title,omitempty"`
	TotalLevels           int    `json:"total_levels"`
	CompletedLevels       int    `json:"completed_levels"`
	SkippedLevels         int    `json:"skipped_levels"`
	FailedLevels          int    `json:"failed_levels"`
	CurrentPosition       int    `json:"current_position,omitempty"`
	CurrentTitle          string `json:"current_title,omitempty"`
}

type Job struct {
	ID         string          `json:"id"`
	Status     string          `json:"status"`
	Phase      string          `json:"phase"`
	Kind       string          `json:"kind"`
	Request    Request         `json:"request"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	ErrorCode  string          `json:"error_code,omitempty"`
	Message    string          `json:"message,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Events     []Event         `json:"events,omitempty"`
	Progress   *BatchProgress  `json:"progress,omitempty"`
}

type Outcome struct {
	Status    string
	Result    json.RawMessage
	ErrorCode string
	Message   string
}

type Executor func(context.Context, Request, func(Event)) (Outcome, error)

type Config struct {
	Path            string
	Workers         int
	QueueSize       int
	PerOwnerPending int
	// PerOwnerRunning limits simultaneous executions for one account. A value
	// of one preserves strict account serialization; web deployments can raise
	// it for independent homework targets.
	PerOwnerRunning int
	Timeout         time.Duration
	Retention       time.Duration
	EventLimit      int
	MaxResultBytes  int
}

type Stats struct {
	Workers         int  `json:"workers"`
	Running         int  `json:"running"`
	Queued          int  `json:"queued"`
	QueueCapacity   int  `json:"queue_size"`
	PerOwnerPending int  `json:"per_owner_pending"`
	PerOwnerRunning int  `json:"per_owner_running"`
	Accepting       bool `json:"accepting"`
}

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string        { return e.Message }
func fail(code, message string) error { return &Error{Code: code, Message: message} }
func IsCode(err error, code string) bool {
	for err != nil {
		if e, ok := err.(*Error); ok {
			return e.Code == code
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			break
		}
		err = u.Unwrap()
	}
	return false
}

// Code extracts a stable API error code without exposing implementation text.
func Code(err error) string {
	if err == nil {
		return ""
	}
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return ""
}

func (c Config) defaults() (Config, error) {
	if c.Path == "" {
		return c, fmt.Errorf("jobs database path is required")
	}
	if c.Workers == 0 {
		c.Workers = 8
	}
	if c.QueueSize == 0 {
		c.QueueSize = 128
	}
	if c.PerOwnerPending == 0 {
		c.PerOwnerPending = 4
	}
	if c.PerOwnerRunning == 0 {
		c.PerOwnerRunning = 1
	}
	if c.Timeout == 0 {
		c.Timeout = 10 * time.Minute
	}
	if c.Retention == 0 {
		c.Retention = 7 * 24 * time.Hour
	}
	if c.EventLimit == 0 {
		c.EventLimit = 64
	}
	if c.MaxResultBytes == 0 {
		c.MaxResultBytes = 256 * 1024
	}
	if c.Workers < 1 || c.Workers > 256 || c.QueueSize < 1 || c.QueueSize > 100000 || c.PerOwnerPending < 1 || c.PerOwnerRunning < 1 || c.PerOwnerRunning > c.Workers || c.Timeout < time.Millisecond || c.Retention < time.Millisecond || c.EventLimit < 1 || c.EventLimit > 64 || c.MaxResultBytes < 1 || c.MaxResultBytes > 4*1024*1024 {
		return c, fmt.Errorf("invalid jobs configuration")
	}
	return c, nil
}
