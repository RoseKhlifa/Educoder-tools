package jobs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
var label = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,80}$`)
var credentialText = regexp.MustCompile(`(?i)(bearer\s+[A-Za-z0-9._~-]+|(?:authorization|cookie|password|passwd|api[_-]?key|access[_-]?token|refresh[_-]?token|sec_key|secret|x-ed[c]?oder-signature)\s*[:=]|(?:[?&])(?:token|signature|sign|access_key|sec_key|secret|x-oss-signature|x-amz-signature)=)`)

func normalize(req Request) (Request, string, string, error) {
	req.Course = strings.TrimSpace(req.Course)
	req.Game = strings.TrimSpace(req.Game)
	collect := req.Mode == "collect_verified"
	if collect && (req.Course != "" || req.HomeworkID != 0 || req.Game != "" || req.Scope != "") {
		return req, "", "", fail("invalid_request", "Collection scans the authenticated account, not a supplied target")
	}
	// Missing scope keeps older clients on the original current-level contract.
	// Canonicalize current to empty so published idempotency payloads remain valid.
	if req.Scope == "current" {
		req.Scope = ""
	}
	if req.Scope != "" && req.Scope != "homework" {
		return req, "", "", fail("invalid_request", "Unknown execution scope")
	}
	if req.Scope == "homework" && (req.Mode != "submit" || req.Game != "") {
		return req, "", "", fail("invalid_request", "Whole-homework jobs require submit mode and no current game")
	}
	if !collect && (!identifier.MatchString(req.Course) || req.HomeworkID < 1 || (req.Game != "" && !identifier.MatchString(req.Game))) {
		return req, "", "", fail("invalid_request", "A valid course, homework_id and optional game are required")
	}
	if !collect && req.Mode != "generate_only" && req.Mode != "selftest" && req.Mode != "submit" {
		return req, "", "", fail("invalid_request", "Unknown execution mode")
	}
	if len(req.IdempotencyKey) > 128 || strings.IndexFunc(req.IdempotencyKey, unicode.IsControl) >= 0 || credentialText.MatchString(req.IdempotencyKey) {
		return req, "", "", fail("invalid_request", "Invalid idempotency key")
	}
	p := req
	p.IdempotencyKey = ""
	b, _ := json.Marshal(p)
	h := sha256.Sum256(b)
	t := struct {
		Course     string
		HomeworkID int64
		Game, Mode string
		Scope      string `json:",omitempty"`
	}{req.Course, req.HomeworkID, req.Game, req.Mode, req.Scope}
	tb, _ := json.Marshal(t)
	th := sha256.Sum256(tb)
	return req, hex.EncodeToString(h[:]), hex.EncodeToString(th[:]), nil
}

func publicText(s, fallback string) string {
	if credentialText.MatchString(s) {
		return fallback
	}
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s))
	if len(s) > 1024 {
		s = s[:1024]
		for !bytes.Equal([]byte(s), []byte(strings.ToValidUTF8(s, ""))) {
			s = s[:len(s)-1]
		}
	}
	if s == "" {
		return fallback
	}
	return s
}

func publicLabel(s, fallback string) string {
	if !label.MatchString(s) {
		return fallback
	}
	return s
}

func publicResult(raw json.RawMessage, limit int) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > limit {
		return nil, fail("result_too_large", "Job result exceeded the storage limit")
	}
	if !json.Valid(raw) {
		return nil, fail("invalid_result", "Executor returned invalid JSON")
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		return nil, fail("invalid_result", "Executor returned invalid JSON")
	}
	if !validatePublic(value, 0) {
		return nil, fail("private_result", "Executor result contained private fields")
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func validatePublic(v any, depth int) bool {
	if depth > 24 {
		return false
	}
	switch x := v.(type) {
	case map[string]any:
		for k, v := range x {
			switch strings.ToLower(strings.ReplaceAll(k, "-", "_")) {
			case "password", "passwd", "cookie", "cookies", "authorization", "api_key", "apikey", "access_token", "refresh_token", "session_cookie", "sec_key", "secret", "token":
				return false
			}
			if !validatePublic(v, depth+1) {
				return false
			}
		}
	case []any:
		for _, v := range x {
			if !validatePublic(v, depth+1) {
				return false
			}
		}
	case string:
		if credentialText.MatchString(x) {
			return false
		}
		if u, e := url.Parse(x); e == nil && u.IsAbs() && u.User != nil {
			return false
		}
	}
	return true
}

func mutatingPhase(phase string) bool {
	p := strings.ToLower(phase)
	return strings.Contains(p, "submitting") || strings.Contains(p, "evaluating") || p == "saving" || p == "submit" || p == "evaluation" || p == "formal_eval" || p == "formal_submit"
}

func clone(j Job) Job {
	j.Result = append(json.RawMessage(nil), j.Result...)
	j.Events = append([]Event(nil), j.Events...)
	for i := range j.Events {
		j.Events[i].Progress = cloneProgress(j.Events[i].Progress)
	}
	j.Progress = cloneProgress(j.Progress)
	if j.StartedAt != nil {
		t := *j.StartedAt
		j.StartedAt = &t
	}
	if j.FinishedAt != nil {
		t := *j.FinishedAt
		j.FinishedAt = &t
	}
	return j
}

func cloneProgress(p *BatchProgress) *BatchProgress {
	if p == nil {
		return nil
	}
	copy := *p
	return &copy
}

func publicProgress(p *BatchProgress) *BatchProgress {
	if p == nil || p.TotalLevels < 1 || p.TotalLevels > 100 || p.CompletedLevels < 0 || p.CompletedLevels > p.TotalLevels || p.SkippedLevels < 0 || p.SkippedLevels > p.CompletedLevels || p.FailedLevels < 0 || p.FailedLevels > p.TotalLevels || p.CurrentPosition < 0 || p.CurrentPosition > p.TotalLevels {
		return nil
	}
	result := *p
	result.CurrentTitle = publicText(result.CurrentTitle, "")
	return &result
}
