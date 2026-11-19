package answer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"educoder-cli/internal/problem"
)

const maxCandidateBytes = 2 << 20

func candidateLanguage(s problem.Snapshot) (string, error) {
	if len(s.Files) == 0 || s.Files[0].Language == "" {
		return "", &Error{Kind: "invalid_problem"}
	}
	language := s.Files[0].Language
	for _, f := range s.Files {
		if f.Language != language {
			return "", &Error{Kind: "mixed_language_contract"}
		}
	}
	return language, nil
}

// ValidateCandidate does not rewrite code or paths. A compiler/evaluator must
// determine semantic correctness; this validates only the output contract.
func ValidateCandidate(snapshot problem.Snapshot, c Candidate) error {
	s, err := problem.Prepare(snapshot)
	if err != nil {
		return &Error{Kind: "invalid_problem"}
	}
	language, err := candidateLanguage(s)
	if err != nil {
		return err
	}
	if c.Kind != "programming" || c.Language != language || len(c.Files) != len(s.Files) {
		return &Error{Kind: "candidate_contract"}
	}
	allowed := make(map[string]bool, len(s.Files))
	for _, f := range s.Files {
		allowed[f.Path] = true
	}
	size := 0
	for _, f := range c.Files {
		if !allowed[f.Path] || strings.TrimSpace(f.Content) == "" {
			return &Error{Kind: "candidate_contract"}
		}
		delete(allowed, f.Path)
		size += len(f.Content) + len(f.Path)
		if size > maxCandidateBytes {
			return &Error{Kind: "candidate_too_large"}
		}
	}
	if _, err := problem.HashFiles(c.Files); err != nil {
		return &Error{Kind: "candidate_contract"}
	}
	return nil
}

func parseCandidate(snapshot problem.Snapshot, content string) (Candidate, error) {
	var c Candidate
	if len(content) > maxCandidateBytes {
		return c, &Error{Kind: "candidate_too_large", OutputShape: describeOutputShape(content, "", programmingOutputKeys...)}
	}
	content = stripJSONFence(strings.TrimSpace(content))
	if err := uniqueJSONKeys(content); err != nil {
		return c, &Error{Kind: "invalid_output_json", OutputShape: describeOutputShape(content, "", programmingOutputKeys...)}
	}
	dec := json.NewDecoder(strings.NewReader(content))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Candidate{}, &Error{Kind: "invalid_output_json", OutputShape: describeOutputShape(content, "", programmingOutputKeys...)}
	}
	if err := ValidateCandidate(snapshot, c); err != nil {
		return Candidate{}, err
	}
	return c, nil
}

var programmingOutputKeys = []string{"kind", "language", "files"}

// stripJSONFence removes only one complete surrounding Markdown JSON fence.
// It never recovers by extracting braces, altering quotes, or interpreting
// extra escape layers.
func stripJSONFence(content string) string {
	for _, prefix := range []string{"```json\n", "```\n", "```json\r\n", "```\r\n"} {
		if strings.HasPrefix(content, prefix) && strings.HasSuffix(content, "\n```") {
			return strings.TrimSuffix(strings.TrimPrefix(content, prefix), "\n```")
		}
	}
	return content
}

// OutputShape is content-free evidence about a rejected or truncated model
// response. It records sizes, whether the text was valid JSON, and which of
// the contract's own field names were present, never values or other keys.
type OutputShape struct {
	ContentBytes   int
	ReasoningBytes int
	Fenced         bool
	ValidJSON      bool
	KnownKeys      []string
	UnknownKeys    int
}

func describeOutputShape(content, reasoning string, known ...string) *OutputShape {
	trimmed := strings.TrimSpace(content)
	shape := &OutputShape{ContentBytes: len(content), ReasoningBytes: len(reasoning), Fenced: strings.HasPrefix(trimmed, "```")}
	body := stripJSONFence(trimmed)
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &object) == nil {
		shape.ValidJSON = true
		allowed := make(map[string]bool, len(known))
		for _, key := range known {
			allowed[key] = true
		}
		for _, key := range known {
			if _, ok := object[key]; ok {
				shape.KnownKeys = append(shape.KnownKeys, key)
			}
		}
		for key := range object {
			if !allowed[key] {
				shape.UnknownKeys++
			}
		}
	}
	return shape
}

func (s *OutputShape) String() string {
	if s == nil {
		return ""
	}
	keys := strings.Join(s.KnownKeys, ",")
	if keys == "" {
		keys = "-"
	}
	return fmt.Sprintf(" [output content_bytes=%d reasoning_bytes=%d fenced=%t valid_json=%t keys=%s unknown_keys=%d]", max(s.ContentBytes, 0), max(s.ReasoningBytes, 0), s.Fenced, s.ValidJSON, keys, max(s.UnknownKeys, 0))
}

// encoding/json accepts duplicate keys by default. Reject them before the
// schema decode so a second kind/path/content cannot replace the first one.
func uniqueJSONKeys(input string) error {
	dec := json.NewDecoder(strings.NewReader(input))
	dec.UseNumber()
	if err := readJSONValue(dec, 0); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

func readJSONValue(dec *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("JSON nesting limit")
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok || seen[key] {
				return errors.New("duplicate or invalid object key")
			}
			seen[key] = true
			if err := readJSONValue(dec, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := readJSONValue(dec, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON closing delimiter")
	}
	closing, err := dec.Token()
	if err != nil || (delim == '{' && closing != json.Delim('}')) || (delim == '[' && closing != json.Delim(']')) {
		return errors.New("invalid JSON closing delimiter")
	}
	return nil
}
