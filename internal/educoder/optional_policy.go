package educoder

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// ErrOptionalTask is returned by an execution-only client when a task reload
// identifies a level as optional. Raw read clients retain optional rows for
// historical inspection and question-bank collection.
var ErrOptionalTask = errors.New("optional homework level is excluded from execution")

// Optionality is explicit assignment metadata, never inferred from a zero
// maximum or the submitted/passed status. Live assignment reads return
// status="optional" for elective levels, while is_choose_todo remains false
// for both required and optional levels and therefore is not a discriminator.
func optionalStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "optional", "选做":
		return true
	default:
		return false
	}
}

func (p ChallengeProgress) IsOptional() bool { return optionalStatus(p.RequiredStatus) }

func (t *Task) IsOptional() bool { return t != nil && optionalStatus(t.RequiredStatus) }

// RequiredChallenges creates a filtered view without modifying the upstream
// roster. Missing or unfamiliar status values stay required.
func (p *HomeworkProgress) RequiredChallenges() []ChallengeProgress {
	if p == nil {
		return nil
	}
	levels := make([]ChallengeProgress, 0, len(p.Challenges))
	for _, level := range p.Challenges {
		if !level.IsOptional() {
			levels = append(levels, level)
		}
	}
	return levels
}

// WithRequiredTasks creates a solver client whose task reloads reject explicit
// optional levels. This keeps a level that changes to optional after preflight
// out of model generation and writes, while leaving the user's raw client and
// shared cookie jar/connection pool intact.
func (c *Client) WithRequiredTasks() (*Client, error) {
	if c == nil || c.HTTP == nil {
		return nil, errors.New("missing execution client context")
	}
	clone := *c
	httpClone := *c.HTTP
	base := httpClone.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	httpClone.Transport = &requiredTaskTransport{base: base}
	clone.HTTP = &httpClone
	return &clone, nil
}

type requiredTaskTransport struct{ base http.RoundTripper }

func (t *requiredTaskTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || req.Method != http.MethodGet || !isTaskDetailPath(req.URL.Path) || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, err
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (16<<20)+1))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	if len(data) > 16<<20 {
		return nil, errors.New("homework task response exceeds limit")
	}
	var task Task
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, err
	}
	if task.IsOptional() {
		return nil, ErrOptionalTask
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))
	return resp, nil
}

func isTaskDetailPath(path string) bool {
	if !strings.HasPrefix(path, "/api/tasks/") || !strings.HasSuffix(path, ".json") {
		return false
	}
	game := strings.TrimSuffix(strings.TrimPrefix(path, "/api/tasks/"), ".json")
	return game != "" && !strings.Contains(game, "/")
}
